package router

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"tclaw/connection"
	"tclaw/dev"
	"tclaw/libraries/logbuffer"
	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/onboarding"
	"tclaw/provider"
	"tclaw/repo"
	"tclaw/schedule"
	"tclaw/tool/bankingtools"
	"tclaw/tool/channeltools"
	"tclaw/tool/connectiontools"
	"tclaw/tool/devtools"
	googletools "tclaw/tool/google"
	"tclaw/tool/modeltools"
	monzotools "tclaw/tool/monzo"
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
	registry  *provider.Registry
	callback  *oauth.CallbackServer // nil if OAuth is not configured
	publicURL string                // externally-reachable base URL, enables Telegram webhooks

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
func New(baseDir string, env config.Env, registry *provider.Registry, callback *oauth.CallbackServer, publicURL string, logBuffer *logbuffer.Buffer, configPath string) *Router {
	return &Router{
		users:      make(map[user.ID]*managedUser),
		mcpServers: make(map[user.ID]*mcp.Server),
		baseDir:    baseDir,
		env:        env,
		registry:   registry,
		callback:   callback,
		publicURL:  publicURL,
		logBuffer:  logBuffer,
		configPath: configPath,
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

	slog.Info("user registered (agent will start on first message)", "user", cfg.ID)
	return nil
}

// waitAndStart blocks until the first message arrives, then starts the
// agent. If the agent exits due to idle timeout, it goes back to waiting
// for the next message and restarts the agent — repeating indefinitely
// until ctx is cancelled.
func (r *Router) waitAndStart(ctx context.Context, mu *managedUser, staticChMap map[channel.ChannelID]channel.Channel, staticMsgs <-chan channel.TaggedMessage) {
	userDir := filepath.Join(r.baseDir, string(mu.cfg.ID))
	homeDir := filepath.Join(userDir, "home")            // Claude Code's territory (HOME env var)
	memoryDir := filepath.Join(userDir, "memory")        // agent's sandboxed memory (CWD + --add-dir)
	stateDir := filepath.Join(userDir, "state")          // tclaw persistent data (not mounted in sandbox)
	sessionsDir := filepath.Join(userDir, "sessions")    // Claude CLI session IDs per channel
	secretsDir := filepath.Join(userDir, "secrets")      // encrypted credentials
	mcpConfigDir := filepath.Join(userDir, "mcp-config") // MCP config files (mounted read-only in sandbox)

	// Create media dir for Telegram file downloads (inside the sandbox).
	if err := os.MkdirAll(filepath.Join(memoryDir, "media"), 0o755); err != nil {
		slog.Error("failed to create media dir", "user", mu.cfg.ID, "err", err)
	}

	// State store for tclaw's own data (connections, remote MCPs, dynamic channels).
	s, err := store.NewFS(stateDir)
	if err != nil {
		slog.Error("failed to create state store", "user", mu.cfg.ID, "err", err)
		return
	}

	// Separate store for session IDs — these bridge tclaw and Claude Code,
	// so they live outside both home/ (Claude's territory) and state/ (tclaw's data).
	sessionStore, err := store.NewFS(sessionsDir)
	if err != nil {
		slog.Error("failed to create session store", "user", mu.cfg.ID, "err", err)
		return
	}

	// Set up secret store for connection credentials.
	secretStore, err := secret.Resolve(string(mu.cfg.ID), secretsDir, os.Getenv(secret.MasterKeyEnv))
	if err != nil {
		slog.Error("failed to create secret store", "user", mu.cfg.ID, "err", err)
		return
	}

	// Set up connection manager and MCP server.
	connMgr := connection.NewManager(s, secretStore)
	mcpHandler := mcp.NewHandler()

	// Track active Google connections so tools can list all available
	// connections in their enum. Shared across connect/disconnect callbacks.
	googleConns := make(map[connection.ConnectionID]googletools.Deps)
	monzoConns := make(map[connection.ConnectionID]monzotools.Deps)

	connectiontools.RegisterTools(mcpHandler, connectiontools.Deps{
		Manager:  connMgr,
		Registry: r.registry,
		Callback: r.callback,
		Handler:  mcpHandler,
		OnProviderConnect: func(connID connection.ConnectionID, mgr *connection.Manager, p *provider.Provider) {
			r.registerToolsForProvider(mcpHandler, connID, mgr, p, googleConns, monzoConns)
		},
		OnProviderDisconnect: func(connID connection.ConnectionID) {
			r.unregisterToolsForProvider(mcpHandler, connID, googleConns, monzoConns)
		},
	})
	if r.callback != nil {
		connectiontools.RegisterAuthWaitTool(mcpHandler, connMgr)
	}

	// Register provider-specific tools for existing connections.
	r.registerProviderTools(ctx, mcpHandler, connMgr, googleConns, monzoConns)

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
		mcps, listErr := connMgr.ListRemoteMCPs(ctx)
		if listErr != nil {
			slog.Error("failed to list remote mcps for config", "err", listErr)
			return nil
		}
		var entries []mcp.RemoteMCPEntry
		for _, m := range mcps {
			entry := mcp.RemoteMCPEntry{Name: m.Name, URL: m.URL}
			auth, authErr := connMgr.GetRemoteMCPAuth(ctx, m.Name)
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
		slog.Info("mcp config regenerated", "user", mu.cfg.ID, "remote_count", len(remotes))
		return nil
	}

	// Register remote MCP management tools.
	remoteMCPDeps := remotemcp.Deps{
		Manager:       connMgr,
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
		slog.Info("found and scrubbed setup token", "user", mu.cfg.ID, "env_var", setupTokenEnvVar)
	}

	// Seed GitHub token from Fly secret (e.g. GITHUB_TOKEN_THEO) into the
	// encrypted secret store. Dev tools read from the store, so this makes
	// pre-provisioned tokens available without runtime prompting. The user
	// can still provide a token interactively via dev_start — it overwrites
	// whatever is in the store.
	githubTokenEnvVar := agent.GitHubTokenEnvVarName(string(mu.cfg.ID))
	if githubToken := os.Getenv(githubTokenEnvVar); githubToken != "" {
		if seedErr := secretStore.Set(ctx, "github_token", githubToken); seedErr != nil {
			slog.Error("failed to seed github token from env", "user", mu.cfg.ID, "err", seedErr)
		} else {
			os.Unsetenv(githubTokenEnvVar)
			slog.Info("seeded and scrubbed github token from env", "user", mu.cfg.ID, "env_var", githubTokenEnvVar)
		}
	}

	// Seed Fly API token from Fly secret (e.g. FLY_TOKEN_THEO) into the
	// encrypted secret store, same pattern as the GitHub token above.
	flyTokenEnvVar := agent.FlyTokenEnvVarName(string(mu.cfg.ID))
	if flyToken := os.Getenv(flyTokenEnvVar); flyToken != "" {
		if seedErr := secretStore.Set(ctx, "fly_api_token", flyToken); seedErr != nil {
			slog.Error("failed to seed fly api token from env", "user", mu.cfg.ID, "err", seedErr)
		} else {
			os.Unsetenv(flyTokenEnvVar)
			slog.Info("seeded and scrubbed fly api token from env", "user", mu.cfg.ID, "env_var", flyTokenEnvVar)
		}
	}

	// Seed TfL API key from Fly secret (e.g. TFL_API_KEY_THEO) into the
	// encrypted secret store, same pattern as the GitHub/Fly tokens above.
	tflKeyEnvVar := agent.TfLAPIKeyEnvVarName(string(mu.cfg.ID))
	if tflKey := os.Getenv(tflKeyEnvVar); tflKey != "" {
		if seedErr := secretStore.Set(ctx, tfltools.APIKeyStoreKey, tflKey); seedErr != nil {
			slog.Error("failed to seed tfl api key from env", "user", mu.cfg.ID, "err", seedErr)
		} else {
			os.Unsetenv(tflKeyEnvVar)
			slog.Info("seeded and scrubbed tfl api key from env", "user", mu.cfg.ID, "env_var", tflKeyEnvVar)
		}
	}

	// Seed Resy API key from Fly secret (e.g. RESY_API_KEY_THEO) into the
	// encrypted secret store, same pattern as the TfL key above.
	resyAPIKeyEnvVar := agent.ResyAPIKeyEnvVarName(string(mu.cfg.ID))
	if resyAPIKey := os.Getenv(resyAPIKeyEnvVar); resyAPIKey != "" {
		if seedErr := secretStore.Set(ctx, restauranttools.ResyAPIKeyStoreKey, resyAPIKey); seedErr != nil {
			slog.Error("failed to seed resy api key from env", "user", mu.cfg.ID, "err", seedErr)
		} else {
			os.Unsetenv(resyAPIKeyEnvVar)
			slog.Info("seeded and scrubbed resy api key from env", "user", mu.cfg.ID, "env_var", resyAPIKeyEnvVar)
		}
	}

	// Seed Resy auth token from Fly secret (e.g. RESY_AUTH_TOKEN_THEO).
	resyAuthTokenEnvVar := agent.ResyAuthTokenEnvVarName(string(mu.cfg.ID))
	if resyAuthToken := os.Getenv(resyAuthTokenEnvVar); resyAuthToken != "" {
		if seedErr := secretStore.Set(ctx, restauranttools.ResyAuthTokenStoreKey, resyAuthToken); seedErr != nil {
			slog.Error("failed to seed resy auth token from env", "user", mu.cfg.ID, "err", seedErr)
		} else {
			os.Unsetenv(resyAuthTokenEnvVar)
			slog.Info("seeded and scrubbed resy auth token from env", "user", mu.cfg.ID, "env_var", resyAuthTokenEnvVar)
		}
	}

	// Seed Enable Banking app ID from Fly secret (e.g. ENABLEBANKING_APP_ID_THEO).
	ebAppIDEnvVar := agent.EnableBankingAppIDEnvVarName(string(mu.cfg.ID))
	if ebAppID := os.Getenv(ebAppIDEnvVar); ebAppID != "" {
		if seedErr := secretStore.Set(ctx, bankingtools.ApplicationIDStoreKey, ebAppID); seedErr != nil {
			slog.Error("failed to seed enable banking app id from env", "user", mu.cfg.ID, "err", seedErr)
		} else {
			os.Unsetenv(ebAppIDEnvVar)
			slog.Info("seeded and scrubbed enable banking app id from env", "user", mu.cfg.ID, "env_var", ebAppIDEnvVar)
		}
	}

	// Seed Enable Banking private key from Fly secret (e.g. ENABLEBANKING_PRIVATE_KEY_THEO).
	ebPrivKeyEnvVar := agent.EnableBankingPrivateKeyEnvVarName(string(mu.cfg.ID))
	if ebPrivKey := os.Getenv(ebPrivKeyEnvVar); ebPrivKey != "" {
		if seedErr := secretStore.Set(ctx, bankingtools.PrivateKeyStoreKey, ebPrivKey); seedErr != nil {
			slog.Error("failed to seed enable banking private key from env", "user", mu.cfg.ID, "err", seedErr)
		} else {
			os.Unsetenv(ebPrivKeyEnvVar)
			slog.Info("seeded and scrubbed enable banking private key from env", "user", mu.cfg.ID, "env_var", ebPrivKeyEnvVar)
		}
	}

	// Seed Telegram Client API credentials from Fly secrets.
	tgAPIIDEnvVar := agent.TelegramClientAPIIDEnvVarName(string(mu.cfg.ID))
	if tgAPIID := os.Getenv(tgAPIIDEnvVar); tgAPIID != "" {
		if seedErr := secretStore.Set(ctx, telegramclient.APIIDStoreKey, tgAPIID); seedErr != nil {
			slog.Error("failed to seed telegram client api id from env", "user", mu.cfg.ID, "err", seedErr)
		} else {
			os.Unsetenv(tgAPIIDEnvVar)
			slog.Info("seeded and scrubbed telegram client api id from env", "user", mu.cfg.ID, "env_var", tgAPIIDEnvVar)
		}
	}
	tgAPIHashEnvVar := agent.TelegramClientAPIHashEnvVarName(string(mu.cfg.ID))
	if tgAPIHash := os.Getenv(tgAPIHashEnvVar); tgAPIHash != "" {
		if seedErr := secretStore.Set(ctx, telegramclient.APIHashStoreKey, tgAPIHash); seedErr != nil {
			slog.Error("failed to seed telegram client api hash from env", "user", mu.cfg.ID, "err", seedErr)
		} else {
			os.Unsetenv(tgAPIHashEnvVar)
			slog.Info("seeded and scrubbed telegram client api hash from env", "user", mu.cfg.ID, "env_var", tgAPIHashEnvVar)
		}
	}

	// Create dynamic channel store — the registry wraps it below.
	dynamicStore := channel.NewDynamicStore(s)

	// channelChangeCh signals the main loop to restart the agent when
	// a channel is created, edited, or deleted via MCP tools.
	channelChangeCh := make(chan struct{}, 1)

	// Build the channel registry — unified view of static + dynamic metadata.
	configByName := make(map[string]config.Channel, len(mu.configChannels))
	for _, cc := range mu.configChannels {
		configByName[cc.Name] = cc
	}
	var staticEntries []channel.RegistryEntry
	for _, info := range channel.InfoAll(staticChMap) {
		entry := channel.RegistryEntry{Info: info}
		if cc, ok := configByName[info.Name]; ok {
			entry.Info.AllowedTools = resolveConfigChannelTools(cc)
			entry.Info.DisallowedTools = cc.DisallowedTools
			entry.Info.CreatableGroups = toolGroupsToStrings(cc.CreatableGroups)
			entry.Info.NotifyLifecycle = cc.NotifyLifecycle
			entry.Links = cc.Links
		}
		staticEntries = append(staticEntries, entry)
	}
	registry := channel.NewRegistry(staticEntries, dynamicStore)
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
	channeltools.RegisterTools(mcpHandler, channeltools.Deps{
		Registry:        registry,
		Env:             r.env,
		SecretStore:     secretStore,
		ConfigPath:      r.configPath,
		ActivityTracker: activityTracker,
		OnChannelChange: onChannelChange,
		Provisioners: map[channel.ChannelType]channel.EphemeralProvisioner{
			channel.TypeTelegram: tgProvisioner,
		},
		ActiveChannel: activeChannelFunc,
	})

	// Set up the scheduler — runs at user lifetime, outlives the agent.
	// When a schedule fires it injects a message that wakes the agent.
	scheduleStore := schedule.NewStore(s)
	scheduleMsgs := make(chan channel.TaggedMessage, 8)

	var currentChannels atomic.Pointer[map[channel.ChannelID]channel.Channel]
	scheduler := schedule.NewScheduler(scheduleStore, scheduleMsgs, func() map[channel.ChannelID]channel.Channel {
		if p := currentChannels.Load(); p != nil {
			return *p
		}
		return nil
	})
	go scheduler.Run(ctx)

	scheduletools.RegisterTools(mcpHandler, scheduletools.Deps{
		Store:     scheduleStore,
		Scheduler: scheduler,
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

	// Register TfL tools unconditionally — they work without an API key
	// (rate-limited to ~50 req/min) and prompt for one if not stored.
	tfltools.RegisterTools(mcpHandler, tfltools.Deps{
		SecretStore: secretStore,
	})

	// Register restaurant tools unconditionally — they check for
	// credentials at call time and return a helpful error if missing.
	restauranttools.RegisterTools(mcpHandler, restauranttools.Deps{
		SecretStore: secretStore,
	})

	// Register banking tools unconditionally — they check for
	// credentials at call time and return a helpful error if missing.
	bankingtools.RegisterTools(mcpHandler, bankingtools.Deps{
		SecretStore: secretStore,
		StateStore:  s,
		Callback:    r.callback,
	})

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
		links, linksErr := registry.Links(ctx)
		if linksErr != nil {
			slog.Error("failed to get links for cross-channel send", "err", linksErr)
			return nil
		}
		return links
	}
	channelsFunc := func() map[channel.ChannelID]channel.Channel {
		if p := currentChannels.Load(); p != nil {
			return *p
		}
		return nil
	}

	channeltools.RegisterSendTool(mcpHandler, channeltools.SendDeps{
		Links:         linksFunc,
		Output:        crossChannelMsgs,
		Channels:      channelsFunc,
		ActiveChannel: activeChannelFunc,
	})

	// Deferred cross-channel delivery — waits until the target is free.
	// Uses a durable queue in the state store so pending messages survive restarts.
	pendingStore := channel.NewPendingStore(s)
	channeltools.RegisterSendWhenFreeTool(mcpHandler, channeltools.SendWhenFreeDeps{
		Links:           linksFunc,
		Output:          crossChannelMsgs,
		Channels:        channelsFunc,
		ActiveChannel:   activeChannelFunc,
		ActivityTracker: activityTracker,
		PendingStore:    pendingStore,
	})

	// Drain goroutine for pending messages. Runs at user lifetime (not agent
	// lifetime) so pending messages are delivered even across agent restarts.
	go drainPendingMessages(ctx, pendingStore, activityTracker, crossChannelMsgs, channelsFunc)

	// Ephemeral channel cleanup goroutine. Runs at user lifetime and
	// periodically tears down ephemeral channels that have been idle past
	// their timeout. Reads from the persistent DynamicStore each tick.
	go cleanupEphemeralChannels(ctx, dynamicStore, activityTracker, secretStore, nil, onChannelChange)

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

	// Merge schedule and cross-channel messages into the static stream so
	// they outlive the agent.
	allStaticMsgs := channel.MergeFanIns(ctx, staticMsgs, scheduleMsgs, crossChannelMsgs)

	firstBoot := true
	for {
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

		// Build dynamic channels from the store each iteration so
		// creates/deletes from the previous agent session take effect.
		dynamicCtx, cancelDynamic := context.WithCancel(ctx)
		dynamicChMap, dynamicMsgs := r.buildDynamicChannels(dynamicCtx, mu.cfg.ID, dynamicStore, secretStore, s)

		// Merge static + dynamic into a combined view for this iteration.
		allChMap := make(map[channel.ChannelID]channel.Channel, len(staticChMap)+len(dynamicChMap))
		for id, ch := range staticChMap {
			allChMap[id] = ch
		}
		for id, ch := range dynamicChMap {
			allChMap[id] = ch
		}

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
		currentChannels.Store(&allChMap)
		scheduler.Reload()

		// Merge message streams.
		mergedMsgs := channel.MergeFanIns(dynamicCtx, allStaticMsgs, dynamicMsgs)

		// Build system prompt inside the loop — channel list may change between iterations.
		var chInfos []agent.ChannelInfo
		for _, ch := range allChMap {
			info := ch.Info()
			chInfos = append(chInfos, agent.ChannelInfo{
				Name:        info.Name,
				Type:        string(info.Type),
				Description: info.Description,
				Source:      string(info.Source),
			})
		}

		// Populate outbound links and compute inbound links by inverting the graph.
		allLinks, _ := registry.Links(dynamicCtx)
		for i, chInfo := range chInfos {
			if links, ok := allLinks[chInfo.Name]; ok {
				for _, link := range links {
					chInfos[i].OutboundLinks = append(chInfos[i].OutboundLinks, agent.ChannelLinkInfo{
						ChannelName: link.Target,
						Description: link.Description,
					})
				}
			}
		}
		for _, chInfo := range chInfos {
			for _, out := range chInfo.OutboundLinks {
				for j, target := range chInfos {
					if target.Name == out.ChannelName {
						chInfos[j].InboundLinks = append(chInfos[j].InboundLinks, agent.ChannelLinkInfo{
							ChannelName: chInfo.Name,
							Description: out.Description,
						})
					}
				}
			}
		}

		// Build dev session info for the system prompt and AddDirs for sandbox access.
		var devSessionInfos []agent.DevSessionInfo
		// Always mount the worktrees parent dir so new worktrees created mid-session
		// (via dev_start) are visible in the sandbox on the very next turn — bwrap
		// can only bind paths that exist at invocation time, so the parent must be
		// present before any child dirs are bound.
		worktreesDir := filepath.Join(userDir, "worktrees")
		if mkErr := os.MkdirAll(worktreesDir, 0o755); mkErr != nil {
			slog.Warn("failed to create worktrees dir", "err", mkErr, "user", mu.cfg.ID)
		}
		// Mount repos dir so read-only checkouts from repo_sync are accessible.
		reposDir := filepath.Join(userDir, "repos")
		if mkErr := os.MkdirAll(reposDir, 0o755); mkErr != nil {
			slog.Warn("failed to create repos dir", "err", mkErr, "user", mu.cfg.ID)
		}
		addDirs := []string{worktreesDir, reposDir}
		devSessions, devErr := devStore.ListSessions(ctx)
		if devErr != nil {
			slog.Error("failed to list dev sessions", "err", devErr)
		}
		for _, sess := range devSessions {
			devSessionInfos = append(devSessionInfos, agent.DevSessionInfo{
				Branch:      sess.Branch,
				WorktreeDir: sess.WorktreeDir,
				Age:         time.Since(sess.CreatedAt).Truncate(time.Minute).String(),
				Stale:       time.Since(sess.CreatedAt) > 4*time.Hour,
			})
			addDirs = append(addDirs, sess.WorktreeDir)
		}

		// Build onboarding info for the system prompt. Initialize state on first
		// encounter — this is idempotent, like seedUserMemory.
		var onboardingInfo *agent.OnboardingInfo
		obState, _, obErr := onboardingStore.Initialize(ctx)
		if obErr != nil {
			slog.Error("failed to initialize onboarding", "user", mu.cfg.ID, "err", obErr)
		}
		if obState != nil && obState.Phase != onboarding.PhaseComplete {
			var missing []string
			for _, f := range onboarding.AllInfoFields {
				if !obState.InfoGathered[f] {
					missing = append(missing, f)
				}
			}
			nextArea := onboarding.NextArea(obState.TipsShown)
			var nextAreaID string
			if nextArea != nil {
				nextAreaID = nextArea.ID
			}
			remaining := onboarding.UnshownAreas(obState.TipsShown)
			var remainingAreas []agent.OnboardingFeatureArea
			for _, area := range remaining {
				remainingAreas = append(remainingAreas, agent.OnboardingFeatureArea{
					ID:          area.ID,
					Name:        area.Name,
					Description: area.Description,
				})
			}
			onboardingInfo = &agent.OnboardingInfo{
				Phase:          string(obState.Phase),
				InfoGathered:   obState.InfoGathered,
				InfoMissing:    missing,
				TipsShown:      len(obState.TipsShown),
				TipsTotal:      len(onboarding.FeatureAreas),
				NextTip:        nextAreaID,
				RemainingAreas: remainingAreas,
			}
		}

		systemPrompt := agent.BuildSystemPrompt(chInfos, devSessionInfos, mu.cfg.SystemPrompt, onboardingInfo)

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
					if ch, ok := allChMap[msg.ChannelID]; ok {
						activityTracker.MessageReceived(ch.Info().Name)
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
		channelToolOverrides := buildChannelToolOverrides(allChMap, registry, dynamicCtx, mu.cfg, connMgr)

		// Generate per-channel MCP config files for channels with scoped remote MCPs.
		mcpConfigPaths := buildMCPConfigPaths(dynamicCtx, allChMap, connMgr, mcpConfigDir, mcpAddr, mcpToken)

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
				dirs := []string{worktreesDir}
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
			Sessions: sessions,
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
				return resetUser(level, memoryDir, homeDir, sessionsDir, stateDir, secretsDir, mcpConfigDir)
			},
			Env:        r.env,
			UserID:     string(mu.cfg.ID),
			SetupToken: setupToken,
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
			slog.Info("agent shut down due to idle timeout, will restart on next message", "user", mu.cfg.ID)
			continue
		}
		if errors.Is(err, agent.ErrResetRequested) {
			slog.Info("agent reset requested, restarting", "user", mu.cfg.ID)
			continue
		}
		if errors.Is(err, agent.ErrChannelChanged) {
			slog.Info("agent restarting after channel change", "user", mu.cfg.ID)
			continue
		}
		if err != nil {
			slog.Error("agent exited with error", "user", mu.cfg.ID, "err", err)
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
	notify, err := registry.LifecycleChannelNames(ctx)
	if err != nil {
		slog.Error("failed to get lifecycle channels", "err", err)
		return
	}

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
// Channels whose Envs list doesn't include env are skipped.
// System-derived paths (socket paths) are computed from the base directory.
func (r *Router) BuildChannels(userID user.ID, channelConfigs []config.Channel, env config.Env, stateStore store.Store) ([]channel.Channel, error) {
	var channels []channel.Channel
	for i, chCfg := range channelConfigs {
		if len(chCfg.Envs) > 0 && !slices.Contains(chCfg.Envs, env) {
			slog.Info("skipping channel (env mismatch)", "channel", chCfg.Name, "envs", chCfg.Envs, "current", env)
			continue
		}

		switch chCfg.Type {
		case config.ChannelTypeSocket:
			if !env.IsLocal() {
				// Socket channels have no authentication — only allow in local dev.
				return nil, fmt.Errorf("channel %d (%s): socket channels are not allowed in %q environment", i, chCfg.Name, env)
			}
			socketPath := filepath.Join(r.baseDir, string(userID), chCfg.Name+".sock")
			channels = append(channels, channel.NewSocketServer(socketPath, chCfg.Name, chCfg.Description))
		case config.ChannelTypeStdio:
			if !env.IsLocal() {
				return nil, fmt.Errorf("channel %d (%s): stdio channels are not allowed in %q environment", i, chCfg.Name, env)
			}
			channels = append(channels, channel.NewStdio())
		case config.ChannelTypeTelegram:
			var opts channel.TelegramOptions
			if r.publicURL != "" && r.callback != nil {
				webhookSecret := make([]byte, 16)
				if _, err := rand.Read(webhookSecret); err != nil {
					return nil, fmt.Errorf("generate webhook path for %s: %w", chCfg.Name, err)
				}
				webhookPath := "/telegram/" + hex.EncodeToString(webhookSecret)
				opts.WebhookURL = r.publicURL + webhookPath
				opts.WebhookPath = webhookPath
				opts.RegisterHandler = func(pattern string, handler http.Handler) {
					r.callback.Handle(pattern, handler)
				}
			}
			opts.ChatID = loadChatID(context.Background(), stateStore, chCfg.Name)
			opts.OnChatID = saveChatIDFunc(stateStore, chCfg.Name)
			opts.MediaDir = filepath.Join(r.baseDir, string(userID), "memory", "media")
			channels = append(channels, channel.NewTelegram(chCfg.TelegramConfig.Token, chCfg.Name, chCfg.Description, chCfg.TelegramConfig.AllowedUsers, opts))
		default:
			return nil, fmt.Errorf("channel %d: unsupported type %q", i, chCfg.Type)
		}
	}
	return channels, nil
}
