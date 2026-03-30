package router

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"tclaw/agent"
	"tclaw/channel"
	"tclaw/claudecli"
	"tclaw/config"
	"tclaw/credential"
	"tclaw/dev"
	"tclaw/libraries/logbuffer"
	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/notification"
	"tclaw/oauth"
	"tclaw/onboarding"
	"tclaw/queue"
	"tclaw/reconciler"
	"tclaw/remotemcpstore"
	"tclaw/repo"
	"tclaw/schedule"
	"tclaw/tool/bankingtools"
	"tclaw/tool/channeltools"
	"tclaw/tool/devtools"
	"tclaw/tool/modeltools"
	"tclaw/tool/notificationtools"
	"tclaw/tool/onboardingtools"
	"tclaw/tool/remotemcp"
	"tclaw/tool/repotools"
	"tclaw/tool/restauranttools"
	"tclaw/tool/scheduletools"
	"tclaw/tool/secretform"
	"tclaw/tool/telegramclient"
	tfltools "tclaw/tool/tfl"
	"tclaw/user"
	"tclaw/version"
)

// Router manages per-user agent goroutines, each with their own
// channels, store, and Claude session. Agents start lazily on
// the first message, not at registration time.
type Router struct {
	mu        sync.Mutex
	users     map[user.ID]*managedUser
	baseDir   string // root for per-user data (home dirs, stores)
	env       config.Env
	callback  *oauth.CallbackServer // nil if OAuth is not configured
	publicURL string                // externally-reachable base URL, enables Telegram webhooks

	// builders maps channel types to their ChannelBuilder implementation.
	builders map[channel.ChannelType]ChannelBuilder

	// configCredentials holds pre-configured credential entries from tclaw.yaml.
	// Seeded into credential sets at startup.
	configCredentials config.CredentialsConfig

	// configPath is the path to the active tclaw.yaml config file. The deploy
	// tool copies it into the git checkout so remote builds include the real
	// config (tclaw.yaml is gitignored, so checkouts only have the example).
	configPath string

	// Per-user MCP servers, keyed by user ID.
	mcpServers map[user.ID]*mcp.Server

	// Shared log ring buffer for the dev_logs MCP tool. May be nil.
	logBuffer *logbuffer.Buffer
}

type managedUser struct {
	cfg      user.Config
	channels []channel.Channel

	// configChannels preserves the raw config.Channel entries so that
	// per-channel tool permissions can be resolved at agent start time.
	configChannels []config.Channel

	// registry is set in waitAndStart so StopAll can look up lifecycle
	// channels. Provides unified access to static + dynamic metadata.
	registry *channel.Registry

	// Set once the agent is running.
	cancel context.CancelFunc
	done   chan struct{}
}

// New creates a Router. baseDir is the root for per-user data — each user
// gets a subdirectory organized into three zones:
//
//	baseDir/
//	  <user-id>/
//	    home/                  -> HOME env var for Claude subprocess
//	      .claude/             -> Claude Code internal state (off limits to agent)
//	        CLAUDE.md          -> symlink to ../../memory/CLAUDE.md
//	        projects/          -> conversation history
//	        settings.json      -> CLI settings
//	    memory/                -> agent's sandbox (CWD + --add-dir)
//	      CLAUDE.md            -> real file, agent's persistent memory
//	      *.md                 -> topic files (@filename.md refs from CLAUDE.md)
//	    state/                 -> tclaw persistent data (connections, remote MCPs, channels) — NOT mounted in sandbox
//	    sessions/              -> Claude CLI session IDs per channel — NOT mounted in sandbox
//	    secrets/               -> encrypted credentials (NaCl secretbox) — NOT mounted in sandbox
//	    mcp-config/            -> MCP config JSON files (mounted read-only in sandbox)
//	    *.sock                 -> unix socket files for channels
//
// Zone 1 (memory/): agent reads/writes freely, sandboxed via CWD + --add-dir.
// Zone 2 (home/.claude/): Claude Code internal state, off limits to agent.
// Zone 3 (state/, sessions/, secrets/): tclaw data, tool-only access via MCP. Not mounted in sandbox.
// Zone 4 (mcp-config/): MCP config files, mounted read-only so the CLI can read --mcp-config.
//
// callback may be nil if OAuth is not configured.
func New(baseDir string, env config.Env, configCredentials config.CredentialsConfig, callback *oauth.CallbackServer, publicURL string, logBuffer *logbuffer.Buffer, configPath string) *Router {
	return &Router{
		users:      make(map[user.ID]*managedUser),
		mcpServers: make(map[user.ID]*mcp.Server),
		baseDir:    baseDir,
		env:        env,
		builders: map[channel.ChannelType]ChannelBuilder{
			channel.TypeSocket:   SocketBuilder{},
			channel.TypeStdio:    StdioBuilder{},
			channel.TypeTelegram: TelegramBuilder{},
		},
		configCredentials: configCredentials,
		callback:          callback,
		publicURL:         publicURL,
		logBuffer:         logBuffer,
		configPath:        configPath,
	}
}

// Register adds a user and its channels to the router without starting
// the agent. Channels begin listening immediately (sockets accept
// connections) but the agent goroutine starts lazily on the first message.
func (r *Router) Register(ctx context.Context, cfg user.Config, channels []channel.Channel, configChannels []config.Channel) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.users[cfg.ID]; exists {
		return fmt.Errorf("user %q already registered", cfg.ID)
	}

	mu := &managedUser{
		cfg:            cfg,
		channels:       channels,
		configChannels: configChannels,
	}
	r.users[cfg.ID] = mu

	// Start listening on all static channels and fan messages into a trigger
	// that starts the agent on the first arrival.
	staticChMap := channel.ChannelMap(channels...)
	staticMsgs := channel.FanIn(ctx, staticChMap)

	go r.waitAndStart(ctx, mu, staticChMap, staticMsgs)

	slog.Info("user registered (agent will start on first message)", "user", cfg.ID, "channels", len(channels))
	return nil
}

// waitAndStart blocks until the first message arrives, then starts the
// agent. If the agent exits due to idle timeout, it goes back to waiting
// for the next message and restarts the agent — repeating indefinitely
// until ctx is cancelled.
func (r *Router) waitAndStart(ctx context.Context, mu *managedUser, staticChMap map[channel.ChannelID]channel.Channel, staticMsgs <-chan channel.TaggedMessage) {
	dirs := NewUserDirs(r.baseDir, string(mu.cfg.ID))

	if err := dirs.EnsureMediaDir(); err != nil {
		slog.Error("failed to create media dir", "user", mu.cfg.ID, "err", err)
	}

	stores, err := NewUserStores(dirs, string(mu.cfg.ID))
	if err != nil {
		slog.Error("failed to create stores", "user", mu.cfg.ID, "err", err)
		return
	}

	// Aliases for backward compat within this function — these will be
	// removed as the remaining inline code is extracted.
	s := stores.State
	sessionStore := stores.Session
	secretStore := stores.Secret
	userDir := dirs.Base
	homeDir := dirs.Home
	memoryDir := dirs.Memory
	mcpConfigDir := dirs.MCPConfig

	// Set up remote MCP manager and credential manager.
	remoteMCPMgr := remotemcpstore.NewManager(s, secretStore)
	credMgr := credential.NewManager(s, secretStore)
	mcpHandler := mcp.NewHandler()

	// Seed config-level credentials into credential sets.
	if err := seedConfigCredentials(ctx, credMgr, r.configCredentials); err != nil {
		slog.Error("failed to seed config credentials", "user", mu.cfg.ID, "err", err)
		return
	}

	mcpServer := mcp.NewServer(mcpHandler)
	mcpToken := mcpServer.Token()
	// Bind to a random port on localhost.
	mcpAddr, err := mcpServer.Start("127.0.0.1:0")
	if err != nil {
		slog.Error("failed to start mcp server", "user", mu.cfg.ID, "err", err)
		return
	}
	defer mcpServer.Stop(context.Background())

	r.mu.Lock()
	r.mcpServers[mu.cfg.ID] = mcpServer
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.mcpServers, mu.cfg.ID)
		r.mu.Unlock()
	}()

	// buildRemoteMCPEntries loads remote MCPs and their auth tokens from
	// the connection manager and returns config entries for the MCP config file.
	buildRemoteMCPEntries := func(ctx context.Context) []mcp.RemoteMCPEntry {
		mcps, listErr := remoteMCPMgr.ListRemoteMCPs(ctx)
		if listErr != nil {
			slog.Error("failed to list remote mcps for config", "err", listErr)
			return nil
		}
		var entries []mcp.RemoteMCPEntry
		for _, m := range mcps {
			entry := mcp.RemoteMCPEntry{Name: m.Name, URL: m.URL}
			auth, authErr := remoteMCPMgr.GetRemoteMCPAuth(ctx, m.Name)
			if authErr != nil {
				slog.Warn("failed to load remote mcp auth", "name", m.Name, "err", authErr)
			}
			if auth != nil && auth.AccessToken != "" {
				entry.BearerToken = auth.AccessToken
			}
			entries = append(entries, entry)
		}
		return entries
	}

	// configUpdater regenerates the MCP config file with current remote MCPs.
	// Called after remote MCPs are added/removed/authorized.
	configUpdater := func(ctx context.Context) error {
		remotes := buildRemoteMCPEntries(ctx)
		_, genErr := mcp.GenerateConfigFile(mcpConfigDir, mcpAddr, mcpToken, remotes)
		if genErr != nil {
			return fmt.Errorf("regenerate mcp config: %w", genErr)
		}
		slog.Debug("mcp config regenerated", "user", mu.cfg.ID, "remote_count", len(remotes))
		return nil
	}

	// Register remote MCP management tools.
	remoteMCPDeps := remotemcp.Deps{
		Manager:       remoteMCPMgr,
		Callback:      r.callback,
		ConfigUpdater: configUpdater,
	}
	remotemcp.RegisterTools(mcpHandler, remoteMCPDeps)
	if r.callback != nil {
		remotemcp.RegisterAuthWaitTool(mcpHandler, remoteMCPDeps)
	}

	// Generate the MCP config file for --mcp-config (includes existing remote MCPs).
	remotes := buildRemoteMCPEntries(ctx)
	mcpConfigPath, err := mcp.GenerateConfigFile(mcpConfigDir, mcpAddr, mcpToken, remotes)
	if err != nil {
		slog.Error("failed to generate mcp config", "user", mu.cfg.ID, "err", err)
		return
	}
	slog.Info("mcp config ready", "user", mu.cfg.ID, "addr", mcpAddr, "config", mcpConfigPath, "remotes", len(remotes))

	// Read per-user setup token from Fly secret (e.g. CLAUDE_SETUP_TOKEN_THEO).
	// Passed to the agent as opts.SetupToken, which buildEnv() maps to
	// CLAUDE_CODE_OAUTH_TOKEN for the claude subprocess.
	setupTokenEnvVar := agent.SetupTokenEnvVarName(string(mu.cfg.ID))
	setupToken := os.Getenv(setupTokenEnvVar)
	if setupToken != "" {
		os.Unsetenv(setupTokenEnvVar)
		slog.Debug("found and scrubbed setup token", "user", mu.cfg.ID, "env_var", setupTokenEnvVar)
	}

	// Seed pre-provisioned secrets from Fly env vars into the encrypted store.
	// Each entry maps a per-user env var (e.g. GITHUB_TOKEN_THEO) to a store
	// key (e.g. "github_token"). The env var is unset after seeding.
	userIDStr := string(mu.cfg.ID)
	secretSeeds := []SecretSeed{
		{EnvVarName: SecretSeedEnvVarName("GITHUB_TOKEN", userIDStr), StoreKey: "github_token"},
		{EnvVarName: SecretSeedEnvVarName("FLY_TOKEN", userIDStr), StoreKey: "fly_api_token"},
		{EnvVarName: SecretSeedEnvVarName("TFL_API_KEY", userIDStr), StoreKey: tfltools.APIKeyStoreKey},
		{EnvVarName: SecretSeedEnvVarName("RESY_API_KEY", userIDStr), StoreKey: restauranttools.ResyAPIKeyStoreKey},
		{EnvVarName: SecretSeedEnvVarName("RESY_AUTH_TOKEN", userIDStr), StoreKey: restauranttools.ResyAuthTokenStoreKey},
		{EnvVarName: SecretSeedEnvVarName("ENABLEBANKING_APP_ID", userIDStr), StoreKey: bankingtools.ApplicationIDStoreKey},
		{EnvVarName: SecretSeedEnvVarName("ENABLEBANKING_PRIVATE_KEY", userIDStr), StoreKey: bankingtools.PrivateKeyStoreKey},
		{EnvVarName: SecretSeedEnvVarName("TELEGRAM_CLIENT_API_ID", userIDStr), StoreKey: telegramclient.APIIDStoreKey},
		{EnvVarName: SecretSeedEnvVarName("TELEGRAM_CLIENT_API_HASH", userIDStr), StoreKey: telegramclient.APIHashStoreKey},
	}
	if err := SeedSecrets(ctx, secretStore, secretSeeds); err != nil {
		slog.Error("failed to seed secrets from env", "user", mu.cfg.ID, "err", err)
	}

	runtimeState := stores.RuntimeState
	configWriter := config.NewWriter(r.configPath, r.env)

	// channelChangeCh signals the main loop to restart the agent when
	// a channel is created, edited, or deleted via MCP tools.
	channelChangeCh := make(chan struct{}, 1)

	// Build the channel registry from config channels.
	registry := channel.NewRegistry(buildRegistryEntries(mu.configChannels))
	mu.registry = registry

	activityTracker := channel.NewActivityTracker()

	// Register Telegram Client API tools early — the returned provisioner
	// is needed by channeltools.RegisterTools for auto-provisioning.
	tgProvisioner := telegramclient.RegisterTools(mcpHandler, telegramclient.Deps{
		SecretStore: secretStore,
		StateStore:  s,
	})

	// activeChannelName tracks which channel is currently processing a turn.
	// Needed by channel_send, channel_send_when_free, and channel_create
	// (for creatable_groups enforcement).
	var activeChannelName atomic.Pointer[string]
	activeChannelFunc := func() string {
		if p := activeChannelName.Load(); p != nil {
			return *p
		}
		return ""
	}

	onChannelChange := func() {
		select {
		case channelChangeCh <- struct{}{}:
		default:
		}
	}
	// Forward-declared so it can be referenced by channeltools.RegisterTools
	// before the full implementation is defined later alongside hotAddMsgs.
	var onChannelAdded func(string)
	// provisioners maps channel types to their EphemeralProvisioner. Defined
	// once here so it can be used by both channeltools (channel_done/channel_create)
	// and the bridge goroutine (interceptPendingDone for async teardown confirmation).
	provisioners := map[channel.ChannelType]channel.EphemeralProvisioner{
		channel.TypeTelegram: tgProvisioner,
	}

	reconcileParams := reconciler.ReconcileParams{
		Channels:     mu.configChannels,
		SecretStore:  secretStore,
		RuntimeState: runtimeState,
		Provisioners: provisioners,
	}

	channeltools.RegisterTools(mcpHandler, channeltools.Deps{
		Registry:        registry,
		ConfigWriter:    configWriter,
		RuntimeState:    runtimeState,
		UserID:          mu.cfg.ID,
		Env:             r.env,
		SecretStore:     secretStore,
		ConfigPath:      r.configPath,
		ActivityTracker: activityTracker,
		OnChannelAdded:  onChannelAdded,
		OnChannelChange: onChannelChange,
		Provisioners:    provisioners,
		ActiveChannel:   activeChannelFunc,
		ReconcileParams: reconcileParams,
	})

	// Set up the scheduler — runs at user lifetime, outlives the agent.
	// When a schedule fires it injects a message that wakes the agent.
	scheduleStore := schedule.NewStore(s)
	scheduleMsgs := make(chan channel.TaggedMessage, 8)

	channelSet := NewChannelSet(nil)

	// Unified message queue — all sources (user, schedule, notification,
	// cross-channel) flow through one queue with source-based priority.
	messageQueue := queue.New(queue.QueueParams{
		Store:    s,
		Activity: activityTracker,
		Channels: channelSet.Snapshot,
	})

	scheduler := schedule.NewScheduler(schedule.SchedulerParams{
		Store:    scheduleStore,
		Output:   scheduleMsgs,
		Channels: channelSet.Snapshot,
	})
	go scheduler.Run(ctx)

	scheduletools.RegisterTools(mcpHandler, scheduletools.Deps{
		Store:     scheduleStore,
		Scheduler: scheduler,
	})

	// Set up the notification manager — runs at user lifetime, outlives the agent.
	// Tool packages register as notifiers, the manager persists subscriptions and
	// routes emitted notifications to the output channel. The unified queue handles
	// busy-channel awareness and source-based priority.
	notificationStore := notification.NewStore(s)
	notificationMsgs := make(chan channel.TaggedMessage, 8)
	notificationManager := notification.NewManager(notification.ManagerParams{
		Store:    notificationStore,
		Output:   notificationMsgs,
		Channels: channelSet.Snapshot,
	})
	go notificationManager.Run(ctx)

	notificationtools.RegisterTools(mcpHandler, notificationtools.Deps{
		Manager: notificationManager,
	})

	// Set up dev workflow tools for code changes, PRs, and deployment.
	devStore := dev.NewStore(s)
	devtools.RegisterTools(mcpHandler, devtools.Deps{
		Store:       devStore,
		SecretStore: secretStore,
		UserDir:     userDir,
		UserID:      mu.cfg.ID,
		LogBuffer:   r.logBuffer,
		ConfigPath:  r.configPath,
	})

	// Set up repo exploration tools for monitoring external repositories.
	repoStore := repo.NewStore(s)
	repotools.RegisterTools(mcpHandler, repotools.Deps{
		Store:       repoStore,
		SecretStore: secretStore,
		UserDir:     userDir,
	})

	// TfL tools work without an API key (rate-limited) — always registered.
	tfltools.RegisterTools(mcpHandler, tfltools.Deps{
		SecretStore: secretStore,
	})

	// Restaurant tools: info/setup tool always visible, operational tools
	// only when credentials are configured.
	restaurantDeps := restauranttools.Deps{
		SecretStore: secretStore,
		OnCredentialsStored: func() {
			restauranttools.RegisterTools(mcpHandler, restauranttools.Deps{SecretStore: secretStore})
			slog.Info("registered restaurant operational tools after credentials stored")
		},
	}
	restauranttools.RegisterInfoTools(mcpHandler, restaurantDeps)
	if hasRestyCredentials(ctx, secretStore) {
		restauranttools.RegisterTools(mcpHandler, restaurantDeps)
	}

	// Banking tools: info/setup tool always visible, operational tools
	// only when credentials are configured.
	bankingDeps := bankingtools.Deps{
		SecretStore: secretStore,
		StateStore:  s,
		Callback:    r.callback,
		OnCredentialsStored: func() {
			bankingtools.RegisterTools(mcpHandler, bankingtools.Deps{
				SecretStore: secretStore,
				StateStore:  s,
				Callback:    r.callback,
			})
			slog.Info("registered banking operational tools after credentials stored")
		},
	}
	bankingtools.RegisterInfoTools(mcpHandler, bankingDeps)
	if hasBankingCredentials(ctx, secretStore) {
		bankingtools.RegisterTools(mcpHandler, bankingDeps)
	}

	// Telegram Client API tools were registered earlier (before channeltools)
	// so the provisioner is available for channel_create auto-provisioning.

	// Set up onboarding state tracking and tools.
	onboardingStore := onboarding.NewStore(s)
	onboardingtools.RegisterTools(mcpHandler, onboardingtools.Deps{
		Store:         onboardingStore,
		ScheduleStore: scheduleStore,
		Scheduler:     scheduler,
	})

	modeltools.RegisterTools(mcpHandler, modeltools.Deps{
		Store: s,
	})

	// Set up cross-channel messaging — lets channels send messages to
	// each other via declared config links. The activeChannelName atomic
	// is set by the agent's OnTurnStart callback before each handle() so
	// the tool can validate from_channel server-side.
	crossChannelMsgs := make(chan channel.TaggedMessage, 8)

	// Shared closures for channel_send and channel_send_when_free.
	// activeChannelFunc was declared earlier (before channeltools registration).
	linksFunc := func() map[string][]channel.Link {
		return registry.Links()
	}
	channelsFunc := channelSet.Snapshot

	channeltools.RegisterSendTool(mcpHandler, channeltools.SendDeps{
		Links:         linksFunc,
		Output:        crossChannelMsgs,
		Channels:      channelsFunc,
		ActiveChannel: activeChannelFunc,
	})

	// Deferred cross-channel delivery — the unified queue handles busy-check
	// and priority, so this tool just injects into the output channel.
	channeltools.RegisterSendWhenFreeTool(mcpHandler, channeltools.SendWhenFreeDeps{
		Links:         linksFunc,
		Output:        crossChannelMsgs,
		Channels:      channelsFunc,
		ActiveChannel: activeChannelFunc,
	})

	// Ephemeral channel cleanup goroutine. Runs at user lifetime and
	// periodically tears down ephemeral channels that have been idle past
	// their timeout. Reads channel config each tick via the config writer.
	go cleanupEphemeralChannels(ctx, mu.cfg.ID, configWriter, runtimeState, activityTracker, secretStore, provisioners, onChannelChange)

	// Register secret form tools for collecting sensitive user input via web forms.
	var secretFormDeps secretform.Deps
	secretFormDeps.SecretStore = secretStore
	if r.callback != nil {
		secretFormDeps.BaseURL = r.callback.BaseURL()
		secretFormDeps.RegisterHandler = func(pattern string, handler http.Handler) {
			r.callback.Handle(pattern, handler)
		}
	}
	secretform.RegisterTools(mcpHandler, secretFormDeps)

	// Register tool_list last so it can see all MCP tools from every package.
	channeltools.RegisterToolListTool(mcpHandler)

	// hotAddMsgs carries messages from channels added mid-session via hot-reload.
	// Lives at user lifetime (like scheduleMsgs) so it outlives agent sessions.
	hotAddMsgs := make(chan channel.TaggedMessage, 8)

	// hotAddCtxRef holds the dynamicCtx for the current loop iteration. Updated
	// at the start of each iteration so onChannelAdded goroutines are bound to
	// the correct context and stop when the agent restarts.
	type ctxHolder struct{ ctx context.Context }
	var hotAddCtxRef atomic.Pointer[ctxHolder]

	// onChannelAdded wires a newly created channel into the running agent without
	// restarting. It builds the channel transport, starts forwarding messages to
	// hotAddMsgs, and updates the ChannelSet so the agent can route responses back.
	onChannelAdded = func(channelName string) {
		// Channel added to config — trigger a full restart so the reconciler
		// can provision it and build the transport.
		slog.Info("channel added, signalling restart", "channel", channelName, "user", mu.cfg.ID)
		onChannelChange()
	}

	// Merge schedule, cross-channel, notification, and hot-add messages into
	// the static stream so they outlive the agent.
	allStaticMsgs := channel.MergeFanIns(ctx, staticMsgs, scheduleMsgs, crossChannelMsgs, hotAddMsgs, notificationMsgs)

	firstBoot := true
	for {
		// Drain any stale channel change signals from a previous iteration.
		// Without this, a change from the previous agent session could fire
		// immediately in the select, starting the 30s force-kill timer before
		// the new agent even processes its first message.
		select {
		case <-channelChangeCh:
		default:
		}

		// Seed memory/CLAUDE.md and the home/.claude/ symlink on each iteration.
		// This is idempotent — only writes if the file/link doesn't exist — and
		// ensures re-seeding after a reset that clears these files.
		seedUserMemory(mu.cfg.ID, memoryDir, homeDir)

		// Regenerate the MCP config on each iteration. ResetAll clears
		// mcp-config/, so the file must be recreated before the next agent spawn.
		remotes := buildRemoteMCPEntries(ctx)
		if p, genErr := mcp.GenerateConfigFile(mcpConfigDir, mcpAddr, mcpToken, remotes); genErr != nil {
			slog.Error("failed to regenerate mcp config", "user", mu.cfg.ID, "err", genErr)
		} else {
			mcpConfigPath = p
		}

		// Reload config and reconcile channels each iteration so changes
		// from the previous agent session (creates/edits/deletes) take effect.
		dynamicCtx, cancelDynamic := context.WithCancel(ctx)
		hotAddCtxRef.Store(&ctxHolder{ctx: dynamicCtx})

		reloadedCfg, reloadErr := configWriter.ReloadConfig()
		if reloadErr != nil {
			slog.Error("failed to reload config, using previous channels", "user", mu.cfg.ID, "err", reloadErr)
		}

		// Use the reloaded config's channels if available.
		var currentChannels []config.Channel
		if reloadedCfg != nil {
			for _, u := range reloadedCfg.Users {
				if u.ID == mu.cfg.ID {
					currentChannels = u.Channels
					break
				}
			}
		}
		if currentChannels == nil {
			currentChannels = mu.configChannels
		}

		// Reconcile: provision channels that need it, identify needs_setup channels.
		reconciled, reconcileErr := reconciler.Reconcile(dynamicCtx, reconciler.ReconcileParams{
			Channels:     currentChannels,
			SecretStore:  secretStore,
			RuntimeState: runtimeState,
			Provisioners: provisioners,
		})
		if reconcileErr != nil {
			slog.Error("channel reconciliation failed", "user", mu.cfg.ID, "err", reconcileErr)
		}

		// Build transports only for ready channels.
		var readyChannels []config.Channel
		for _, rc := range reconciled {
			if rc.Status == reconciler.ChannelReady {
				readyChannels = append(readyChannels, rc.Config)
			}
		}

		// Update registry with all channels (including needs_setup for visibility).
		registry.Reload(buildRegistryEntries(currentChannels))

		// Build live channel transports from ready channels.
		// Find the user config for this iteration (need Telegram user ID etc.)
		var currentUserCfg config.User
		if reloadedCfg != nil {
			for _, u := range reloadedCfg.Users {
				if u.ID == mu.cfg.ID {
					currentUserCfg = u
					break
				}
			}
		}

		liveChannels, buildErr := r.BuildChannels(dynamicCtx, BuildChannelsParams{
			UserID:      mu.cfg.ID,
			UserCfg:     currentUserCfg,
			Channels:    readyChannels,
			Env:         r.env,
			StateStore:  s,
			SecretStore: secretStore,
		})
		if buildErr != nil {
			slog.Error("failed to build channels", "user", mu.cfg.ID, "err", buildErr)
		}
		allChMap := channel.ChannelMap(liveChannels...)
		// Also include the initial static channels so existing listeners are reachable.
		for id, ch := range staticChMap {
			if _, exists := allChMap[id]; !exists {
				allChMap[id] = ch
			}
		}

		// Inject initial_message for any newly created channels.
		injectInitialMessages(dynamicCtx, mu.cfg.ID, configWriter, allChMap, crossChannelMsgs)

		// Start listening on channels built this iteration (excludes static
		// channels which are already listening via staticMsgs).
		dynamicMsgs := channel.FanIn(dynamicCtx, channel.ChannelMap(liveChannels...))

		// isRestart is true on every iteration after the first — i.e. whenever
		// the agent has restarted (idle timeout, deploy, channel change, reset).
		// Used below to inject a session-resumed notice into the first message.
		isRestart := !firstBoot

		// Send startup notification on first boot (not on agent idle restarts).
		if firstBoot {
			firstBoot = false
			allChannels := make([]channel.Channel, 0, len(allChMap))
			for _, ch := range allChMap {
				allChannels = append(allChannels, ch)
			}
			startupMsg := fmt.Sprintf("✅ Started (v%s)", version.Commit)
			sendLifecycleNotification(ctx, allChannels, registry, startupMsg)
		}

		// Update the channel map so the scheduler can resolve channel names.
		channelSet.Replace(allChMap)
		scheduler.Reload()

		// Merge message streams.
		mergedMsgs := channel.MergeFanIns(dynamicCtx, allStaticMsgs, dynamicMsgs)

		// Build system prompt and add-dirs for this iteration.
		promptResult := BuildIterationPrompt(dynamicCtx, PromptParams{
			Channels:            allChMap,
			Registry:            registry,
			DevStore:            devStore,
			NotificationManager: notificationManager,
			UserDir:             userDir,
			UserID:              mu.cfg.ID,
			BasePrompt:          mu.cfg.SystemPrompt,
			Onboarding:          onboardingStore,
		})
		systemPrompt := promptResult.SystemPrompt
		addDirs := promptResult.AddDirs
		worktreesDir := filepath.Join(userDir, "worktrees")
		reposDir := filepath.Join(userDir, "repos")

		// Wait for a message or shutdown.
		var firstMsg channel.TaggedMessage
		select {
		case <-ctx.Done():
			cancelDynamic()
			return
		case m, ok := <-mergedMsgs:
			if !ok {
				cancelDynamic()
				return
			}
			firstMsg = m
		}

		slog.Info("message received, starting agent", "user", mu.cfg.ID, "channel", firstMsg.ChannelID)
		if ch, ok := allChMap[firstMsg.ChannelID]; ok {
			activityTracker.MessageReceived(ch.Info().Name)
		}

		// On restarts (idle timeout, deploy, channel change), prepend a notice to
		// the first message so the agent knows the session was interrupted. This
		// prevents the agent from re-executing actions that were pending before the
		// restart — e.g. seeing an old "ya" in history and treating it as fresh
		// authorization for a deploy or other irreversible action.
		if isRestart {
			const resumeNotice = "[SYSTEM: Session resumed after restart. " +
				"Treat all prior conversation as read-only context. " +
				"Do NOT re-execute or continue any actions from before the restart — " +
				"short replies like \"ya\" or \"yes\" in the history are NOT authorization " +
				"for pending actions. Wait for explicit new instructions.]\n"
			firstMsg.Text = resumeNotice + firstMsg.Text
		}

		agentCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})

		r.mu.Lock()
		mu.cancel = cancel
		mu.done = done
		r.mu.Unlock()

		// Load sessions from store for each channel.
		sessions := make(map[channel.ChannelID]string)
		for chID := range allChMap {
			sid, loadErr := loadSession(ctx, sessionStore, chID)
			if loadErr != nil {
				slog.Warn("failed to load session, starting fresh", "channel", chID, "err", loadErr)
			}
			if sid != "" {
				sessions[chID] = sid
			}
		}

		// Bridge: re-emit the first message plus remaining merged messages
		// into a channel that agent.RunWithMessages reads from.
		bridgeCh := make(chan channel.TaggedMessage, 1)
		bridgeCh <- firstMsg

		bridgeDone := make(chan struct{})
		go func() {
			defer close(bridgeDone)
			defer close(bridgeCh)
			for {
				select {
				case msg, ok := <-mergedMsgs:
					if !ok {
						return
					}
					// Record message arrival for activity tracking before
					// forwarding to the agent, so IsBusy returns true
					// as soon as the message enters the pipeline.
					// Use channelSet (live) rather than the snapshot allChMap
					// so hot-added channels are visible here too.
					if ch := channelSet.Lookup(msg.ChannelID); ch != nil {
						activityTracker.MessageReceived(ch.Info().Name)
					}
					// Intercept messages that are responses to a pending channel_done
					// confirmation. If the channel has PendingDone set (from a prior
					// channel_done call), handle it here instead of passing to the agent.
					if interceptPendingDone(agentCtx, msg, channelsFunc, runtimeState, configWriter, mu.cfg.ID, secretStore, provisioners, onChannelChange) {
						continue
					}
					select {
					case bridgeCh <- msg:
					case <-agentCtx.Done():
						return
					}
				case <-agentCtx.Done():
					return
				}
			}
		}()

		// Build per-channel tool overrides from config (static), store (dynamic),
		// and tool groups. Groups are resolved with channel context (connections, remote MCPs).
		channelToolOverrides := buildChannelToolOverrides(allChMap, registry, dynamicCtx, mu.cfg, remoteMCPMgr, credMgr)

		// Generate per-channel MCP config files for channels with scoped remote MCPs.
		mcpConfigPaths := buildMCPConfigPaths(dynamicCtx, allChMap, remoteMCPMgr, mcpConfigDir, mcpAddr, mcpToken)

		// channelChangeNotify is closed when a channel change fires,
		// telling the agent to finish its current turn then exit.
		// Created per iteration because a closed channel can't be reused.
		channelChangeNotify := make(chan struct{})

		opts := agent.Options{
			PermissionMode: mu.cfg.PermissionMode,
			Model:          mu.cfg.Model,
			ModelFunc: func() claudecli.Model {
				return modeltools.LoadModel(s, mu.cfg.Model)
			},
			MaxTurns:  mu.cfg.MaxTurns,
			Debug:     mu.cfg.Debug,
			APIKey:    mu.cfg.APIKey,
			HomeDir:   homeDir,
			MemoryDir: memoryDir,
			AddDirs:   addDirs,
			AddDirsFunc: func() []string {
				// Read from the dev store each turn so worktrees created
				// mid-session (via dev_start) are immediately accessible.
				// Always include the parent worktrees dir so bwrap can bind it.
				dirs := []string{worktreesDir, reposDir}
				sessions, err := devStore.ListSessions(ctx)
				if err != nil {
					slog.Error("failed to list dev sessions for add-dirs", "err", err)
					return dirs
				}
				for _, sess := range sessions {
					dirs = append(dirs, sess.WorktreeDir)
				}
				return dirs
			},
			Channels: allChMap,
			// ChannelsFunc provides live channel lookups so hot-added channels
			// are reachable by the agent without restarting.
			ChannelsFunc: channelsFunc,
			Sessions:     sessions,
			Queue:        messageQueue,
			OnSessionUpdate: func(chID channel.ChannelID, sessionID string) {
				if saveErr := saveSession(ctx, sessionStore, chID, sessionID); saveErr != nil {
					slog.Error("failed to save session", "err", saveErr)
				}
			},
			OnTurnStart: func(channelName string) {
				activeChannelName.Store(&channelName)
				activityTracker.TurnStarted(channelName)
			},
			OnTurnEnd: func(channelName string) {
				activityTracker.TurnEnded(channelName)
			},
			AllowedTools:         mu.cfg.AllowedTools,
			DisallowedTools:      mu.cfg.DisallowedTools,
			ChannelToolOverrides: channelToolOverrides,
			MCPConfigPath:        mcpConfigPath,
			MCPConfigPaths:       mcpConfigPaths,
			// Live query so globs expand against tools registered mid-session
			// (e.g. Google tools added after OAuth connection).
			MCPToolNames: func() []string {
				tools := mcpHandler.ListTools()
				names := make([]string, len(tools))
				for i, td := range tools {
					names[i] = td.Name
				}
				return names
			},
			SystemPrompt:    systemPrompt,
			SecretStore:     secretStore,
			ChannelChangeCh: channelChangeNotify,
			OnReset: func(level agent.ResetLevel) error {
				return resetUser(level, dirs.Memory, dirs.Home, dirs.Sessions, dirs.State, dirs.Secrets, dirs.MCPConfig)
			},
			Env:           r.env,
			UserID:        string(mu.cfg.ID),
			SetupToken:    setupToken,
			HasProdConfig: config.HasEnv(r.configPath, config.EnvProd),
		}

		agentErr := make(chan error, 1)
		go func() {
			defer close(done)
			agentErr <- agent.RunWithMessages(agentCtx, opts, bridgeCh)
		}()

		// Wait for agent to finish, or for a channel change signal.
		// The bridge must exit before we loop back to reading mergedMsgs,
		// otherwise both the bridge and the main loop race to read
		// from the same channel and the bridge drops the message.
		var err error
		select {
		case err = <-agentErr:
			// Agent exited normally.
		case <-channelChangeCh:
			// Channel created/edited/deleted — let the agent finish its
			// current turn so it can send a restart notice, then exit.
			slog.Info("channel changed, waiting for agent to finish turn", "user", mu.cfg.ID)
			close(channelChangeNotify)
			select {
			case err = <-agentErr:
				// Agent finished the turn and exited gracefully.
			case <-time.After(30 * time.Second):
				// Safety timeout — force cancel if the turn is stuck.
				slog.Warn("agent did not exit after channel change, forcing", "user", mu.cfg.ID)
				cancel()
				err = <-agentErr
			}
		}

		cancel()
		<-done
		<-bridgeDone

		// Cancel dynamic channels so their listeners/sockets close.
		// Next iteration will recreate them from the (possibly updated) store.
		cancelDynamic()

		r.mu.Lock()
		mu.cancel = nil
		mu.done = nil
		r.mu.Unlock()

		if errors.Is(err, agent.ErrIdleTimeout) {
			slog.Info("agent restarting", "user", mu.cfg.ID, "reason", "idle_timeout")
			// Idle timeout means the agent wasn't doing anything — don't resume.
			if clearErr := messageQueue.ClearInterrupted(ctx); clearErr != nil {
				slog.Error("failed to clear interrupted marker on idle timeout", "err", clearErr)
			}
			continue
		}
		if errors.Is(err, agent.ErrResetRequested) {
			slog.Info("agent restarting", "user", mu.cfg.ID, "reason", "reset")
			// User explicitly reset — don't resume old work.
			if clearErr := messageQueue.ClearInterrupted(ctx); clearErr != nil {
				slog.Error("failed to clear interrupted marker on reset", "err", clearErr)
			}
			continue
		}
		if errors.Is(err, agent.ErrChannelChanged) {
			// Channel changed mid-session — the interrupted marker (if set) is
			// preserved so the agent resumes the interrupted turn on restart.
			slog.Info("agent restarting", "user", mu.cfg.ID, "reason", "channel_change")
			continue
		}
		if err != nil {
			slog.Error("agent exited with error", "user", mu.cfg.ID, "reason", "error", "err", err)
		}
		return
	}
}

// StopUser cancels a user's agent and waits for it to finish.
func (r *Router) StopUser(userID user.ID) {
	r.mu.Lock()
	u, ok := r.users[userID]
	if !ok {
		r.mu.Unlock()
		return
	}
	// Copy cancel/done inside the lock to avoid racing with waitAndStart
	// which nils these out after the agent exits.
	cancel := u.cancel
	done := u.done
	delete(r.users, userID)
	r.mu.Unlock()

	// Agent may not have started yet.
	if cancel != nil {
		cancel()
		<-done
	}
	slog.Info("user stopped", "user", userID)
}

// StopAll cancels all users and waits for them to finish.
// Sends shutdown notifications to channels with NotifyLifecycle before stopping.
func (r *Router) StopAll() {
	r.mu.Lock()
	users := make(map[user.ID]*managedUser, len(r.users))
	maps.Copy(users, r.users)
	r.users = make(map[user.ID]*managedUser)
	r.mu.Unlock()

	// Send shutdown notifications before cancelling agents.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg := fmt.Sprintf("🔄 Shutting down (v%s)...", version.Commit)
	for _, u := range users {
		if u.registry != nil {
			sendLifecycleNotification(shutdownCtx, u.channels, u.registry, msg)
		}
	}

	for id, u := range users {
		if u.cancel != nil {
			u.cancel()
			<-u.done
		}
		slog.Info("user stopped", "user", id)
	}
}

// sendLifecycleNotification sends a message to all channels that have
// NotifyLifecycle enabled.
func sendLifecycleNotification(ctx context.Context, channels []channel.Channel, registry *channel.Registry, message string) {
	notify := registry.LifecycleChannelNames()

	for _, ch := range channels {
		if !notify[ch.Info().Name] {
			continue
		}
		if _, sendErr := ch.Send(ctx, message); sendErr != nil {
			slog.Warn("failed to send lifecycle notification", "channel", ch.Info().Name, "err", sendErr)
		}
	}
}

// BuildChannels creates channel instances from config for a given user.
// Dispatches to registered ChannelBuilder implementations by type.
// Channels whose Envs list doesn't include env are skipped.
type BuildChannelsParams struct {
	UserID      user.ID
	UserCfg     config.User
	Channels    []config.Channel
	Env         config.Env
	StateStore  store.Store
	SecretStore secret.Store
}

func (r *Router) BuildChannels(ctx context.Context, params BuildChannelsParams) ([]channel.Channel, error) {
	var channels []channel.Channel
	for _, chCfg := range params.Channels {
		if len(chCfg.Envs) > 0 && !slices.Contains(chCfg.Envs, params.Env) {
			slog.Info("skipping channel (env mismatch)", "channel", chCfg.Name, "envs", chCfg.Envs, "current", params.Env)
			continue
		}

		builder, ok := r.builders[chCfg.Type]
		if !ok {
			return nil, fmt.Errorf("channel %q: unsupported type %q", chCfg.Name, chCfg.Type)
		}

		var registerHandler func(string, http.Handler)
		if r.callback != nil {
			registerHandler = r.callback.Handle
		}

		ch, err := builder.Build(ctx, ChannelBuildParams{
			ChannelCfg:      chCfg,
			UserCfg:         params.UserCfg,
			UserID:          params.UserID,
			Env:             params.Env,
			BaseDir:         r.baseDir,
			SecretStore:     params.SecretStore,
			StateStore:      params.StateStore,
			PublicURL:       r.publicURL,
			RegisterHandler: registerHandler,
		})
		if err != nil {
			return nil, fmt.Errorf("channel %q: %w", chCfg.Name, err)
		}
		channels = append(channels, ch)
	}
	return channels, nil
}

// hasRestyCredentials checks if Resy API credentials are stored.
func hasRestyCredentials(ctx context.Context, s secret.Store) bool {
	key, err := s.Get(ctx, restauranttools.ResyAPIKeyStoreKey)
	if err != nil {
		slog.Warn("failed to check Resy API key", "err", err)
		return false
	}
	token, err := s.Get(ctx, restauranttools.ResyAuthTokenStoreKey)
	if err != nil {
		slog.Warn("failed to check Resy auth token", "err", err)
		return false
	}
	return key != "" && token != ""
}

// hasBankingCredentials checks if Enable Banking credentials are stored.
func hasBankingCredentials(ctx context.Context, s secret.Store) bool {
	appID, err := s.Get(ctx, bankingtools.ApplicationIDStoreKey)
	if err != nil {
		slog.Warn("failed to check Enable Banking app ID", "err", err)
		return false
	}
	privKey, err := s.Get(ctx, bankingtools.PrivateKeyStoreKey)
	if err != nil {
		slog.Warn("failed to check Enable Banking private key", "err", err)
		return false
	}
	return appID != "" && privKey != ""
}
