// Package router is the top-level per-user orchestrator and the only stateful struct in the system.
// It manages agent goroutine lifecycles, per-user directory setup, MCP server creation, tool
// registration, channel building via the channelpkg registry, and secret seeding from Fly env vars.
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

	"tclaw/internal/agent"
	"tclaw/internal/channel"
	channelall "tclaw/internal/channel/all"
	"tclaw/internal/channel/channelpkg"
	"tclaw/internal/channel/outbox"
	"tclaw/internal/claudecli"
	"tclaw/internal/config"
	"tclaw/internal/credential"
	"tclaw/internal/dev"
	"tclaw/internal/libraries/logbuffer"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/notification"
	"tclaw/internal/oauth"
	"tclaw/internal/onboarding"
	"tclaw/internal/queue"
	"tclaw/internal/reconciler"
	"tclaw/internal/remotemcpproxy"
	"tclaw/internal/remotemcpstore"
	"tclaw/internal/repo"
	"tclaw/internal/schedule"
	"tclaw/internal/tool/all"
	"tclaw/internal/tool/modeltools"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
	"tclaw/internal/user"
	"tclaw/internal/version"
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

	// channelRegistry maps channel types to their package implementations.
	channelRegistry *channelpkg.Registry

	// credentialSlots holds the credential slots declared in tclaw.yaml.
	// Seeded into credential sets at startup.
	credentialSlots []config.CredentialSlot

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
	cfg user.Config

	// configChannels preserves the raw config.Channel entries so that
	// per-channel tool permissions can be resolved at agent start time.
	configChannels []config.Channel

	// channelSet is the live channel map, set in waitAndStart. StopAll
	// reads it to send shutdown notifications.
	channelSet *ChannelSet

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
func New(baseDir string, env config.Env, credentialSlots []config.CredentialSlot, callback *oauth.CallbackServer, publicURL string, logBuffer *logbuffer.Buffer, configPath string) *Router {
	return &Router{
		users:      make(map[user.ID]*managedUser),
		mcpServers: make(map[user.ID]*mcp.Server),
		baseDir:    baseDir,
		env:        env,
		// Provisioner is nil here — set per-user in startUser after telegramclient.RegisterTools.
		channelRegistry: channelall.NewRegistry(nil),
		credentialSlots: credentialSlots,
		callback:        callback,
		publicURL:       publicURL,
		logBuffer:       logBuffer,
		configPath:      configPath,
	}
}

// Register adds a user and its channels to the router without starting
// the agent. Channels begin listening immediately (sockets accept
// connections) but the agent goroutine starts lazily on the first message.
func (r *Router) Register(ctx context.Context, cfg user.Config, configChannels []config.Channel) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.users[cfg.ID]; exists {
		return fmt.Errorf("user %q already registered", cfg.ID)
	}

	mu := &managedUser{
		cfg:            cfg,
		configChannels: configChannels,
	}
	r.users[cfg.ID] = mu

	// All channels are built lazily in waitAndStart — no pre-built
	// channels are passed, so start with an empty static map.
	staticChMap := channel.ChannelMap()
	staticMsgs := channel.FanIn(ctx, staticChMap)

	go r.waitAndStart(ctx, mu, staticChMap, staticMsgs)

	slog.Info("user registered (agent will start on first message)", "user", cfg.ID, "channels", len(configChannels))
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
	sessionStore := channel.NewSessionStore(stores.Session)
	secretStore := stores.Secret
	userDir := dirs.Base
	homeDir := dirs.Home
	memoryDir := dirs.Memory
	mcpConfigDir := dirs.MCPConfig

	// Set up remote MCP manager and credential manager.
	remoteMCPMgr := remotemcpstore.NewManager(s, secretStore)
	credMgr := credential.NewManager(s, secretStore)
	mcpHandler := mcp.NewHandler()

	// Create a credential set for every declared slot, filling any values config supplies.
	if err := seedCredentialSlots(ctx, credMgr, r.credentialSlots); err != nil {
		slog.Error("failed to seed credential slots", "user", mu.cfg.ID, "err", err)
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

	// Remote-MCP auth proxy: fronts every connected remote MCP server on localhost
	// and injects each server's credentials server-side, so tokens never enter the
	// sandbox-readable --mcp-config. Same rationale as the knowledge proxy for git.
	remoteMCPProxy, err := remotemcpproxy.NewServer(remotemcpproxy.Config{Store: remoteMCPMgr})
	if err != nil {
		slog.Error("failed to create remote mcp proxy", "user", mu.cfg.ID, "err", err)
		return
	}
	proxyToken := remoteMCPProxy.Token()
	if _, startErr := remoteMCPProxy.Start("127.0.0.1:0"); startErr != nil {
		slog.Error("failed to start remote mcp proxy", "user", mu.cfg.ID, "err", startErr)
		return
	}
	defer remoteMCPProxy.Stop(context.Background())

	// Personal knowledge base: start the per-user git-auth proxy and provision the
	// vault clone. The proxy injects the GitHub token server-side, so the agent can
	// pull/push via raw git without the token entering its subprocess. Failures are
	// logged, not fatal — the user session continues without the knowledge base.
	if kc := mu.cfg.Knowledge; kc != nil {
		if knowledgeProxy, kpErr := startKnowledgeProxy(mu.cfg.ID, kc, secretStore); kpErr != nil {
			slog.Error("failed to start knowledge proxy", "user", mu.cfg.ID, "err", kpErr)
		} else {
			defer knowledgeProxy.Stop(context.Background())
			if provErr := provisionKnowledgeClone(knowledgeProvisionParams{
				Dir:         dirs.Knowledge,
				RemoteURL:   knowledgeProxy.RemoteURL(),
				Branch:      kc.Branch,
				CommitName:  kc.CommitName,
				CommitEmail: kc.CommitEmail,
			}); provErr != nil {
				slog.Error("failed to provision knowledge clone", "user", mu.cfg.ID, "err", provErr)
			}
		}
	}

	// buildRemoteMCPEntries lists the connected remote MCPs and returns config
	// entries pointing at the auth proxy. Credentials are injected by the proxy
	// at request time, so none are read (or written to the config) here.
	buildRemoteMCPEntries := func(ctx context.Context) []mcp.RemoteMCPEntry {
		mcps, listErr := remoteMCPMgr.ListRemoteMCPs(ctx)
		if listErr != nil {
			slog.Error("failed to list remote mcps for config", "err", listErr)
			return nil
		}
		return remoteMCPConfigEntries(mcps, remoteMCPProxy, proxyToken)
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

	runtimeState := stores.RuntimeState
	configWriter := config.NewWriter(r.configPath, r.env)

	// channelChangeCh signals the main loop to restart the agent when
	// a channel is created, edited, or deleted via MCP tools.
	channelChangeCh := make(chan struct{}, 1)

	// Build the channel registry from config channels.
	registry := channel.NewRegistry(buildRegistryEntries(mu.configChannels))
	mu.registry = registry

	// Persisted tracker so ephemeral cleanup decisions survive restarts.
	// Load last-message timestamps for all configured channels.
	channelNames := make([]string, len(mu.configChannels))
	for i, cc := range mu.configChannels {
		channelNames[i] = cc.Name
	}
	activityTracker := channel.NewPersistedActivityTracker(ctx, runtimeState, channelNames)

	// activeChannelName tracks which channel is currently processing a turn.
	// Needed by channel_send and channel_create
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
	// Forward-declared so it can be referenced by channel tools during Register()
	// before the full implementation is defined later alongside hotAddMsgs.
	var onChannelAdded func(string)

	// Set up the scheduler — runs at user lifetime, outlives the agent.
	// When a schedule fires it injects a message that wakes the agent.
	scheduleStore := schedule.NewStore(s)
	scheduleMsgs := make(chan channel.TaggedMessage, 8)

	channelSet := NewChannelSet(nil)
	mu.channelSet = channelSet

	// Unified message queue — all sources (user, schedule, notification,
	// cross-channel) flow through one queue with source-based priority.
	messageQueue := queue.New(queue.QueueParams{
		Store:    s,
		Activity: activityTracker,
		Channels: channelSet.Snapshot,
		// Coalesce bursts of same-channel user messages (e.g. a photo album that
		// arrives as separate messages) into one turn; control commands are never
		// batched, so the classifier is injected here (queue can't import agent).
		DebounceWindow:   mu.cfg.MessageDebounce,
		IsControlMessage: func(m channel.TaggedMessage) bool { return agent.IsControlCommand(m.Text) },
	})

	// Outbound message queue — all Send/Edit/Done calls go through the outbox
	// so the agent loop never blocks on channel I/O (e.g. Telegram API timeouts).
	messageOutbox := outbox.New(outbox.Params{
		Store:    s,
		Channels: channelSet.Snapshot,
	})

	scheduler := schedule.NewScheduler(schedule.SchedulerParams{
		Store:    scheduleStore,
		Output:   scheduleMsgs,
		Channels: channelSet.Snapshot,
	})
	go scheduler.Run(ctx)

	// Set up the notification manager — runs at user lifetime, outlives the agent.
	// The ready channel is closed after credential system init so notifiers are
	// registered before the manager tries to resubscribe persisted subscriptions.
	notificationStore := notification.NewStore(s)
	notificationMsgs := make(chan channel.TaggedMessage, 8)
	notifiersReady := make(chan struct{})
	notificationManager := notification.NewManager(notification.ManagerParams{
		Store:    notificationStore,
		Output:   notificationMsgs,
		Channels: channelSet.Snapshot,
		Ready:    notifiersReady,
	})
	go notificationManager.Run(ctx)

	devStore := dev.NewStore(s)
	// Seed the app URL so dev_deployed can hit /version without manual config.
	if r.publicURL != "" {
		if err := devStore.SetAppURL(ctx, r.publicURL); err != nil {
			slog.Warn("failed to seed app URL in dev store", "err", err)
		}
	}
	repoStore := repo.NewStore(s)
	onboardingStore := onboarding.NewStore(s)

	// Clone the repos declared in config so they're on disk before the first
	// turn. Non-fatal: a repo that can't be fetched is logged and the session
	// continues without it.
	if len(mu.cfg.Repos) > 0 {
		if err := provisionConfigRepos(ctx, reposProvisionParams{
			UserID:  mu.cfg.ID,
			UserDir: userDir,
			Repos:   mu.cfg.Repos,
			Store:   repoStore,
			Secrets: secretStore,
		}); err != nil {
			slog.Error("failed to provision config repos", "user", mu.cfg.ID, "err", err)
		}
	}

	// Set up cross-channel messaging — lets channels send messages to
	// each other via declared config links.
	crossChannelMsgs := make(chan channel.TaggedMessage, 8)
	linksFunc := func() map[string][]channel.Link {
		return registry.Links()
	}
	channelsFunc := channelSet.Snapshot

	// Build the secret form deps — base URL and handler registration come
	// from the callback server when available.
	var secretFormBaseURL string
	var secretFormRegisterHandler func(string, http.Handler)
	if r.callback != nil {
		secretFormBaseURL = r.callback.BaseURL()
		secretFormRegisterHandler = func(pattern string, handler http.Handler) {
			r.callback.Handle(pattern, handler)
		}
	}

	// Build the tool package registry with all deps populated on each package.
	toolRegistry, provisioners := all.NewRegistry(all.Params{
		SecretStore:         secretStore,
		StateStore:          s,
		Callback:            r.callback,
		UserDir:             userDir,
		UserID:              mu.cfg.ID,
		Env:                 r.env,
		ConfigPath:          r.configPath,
		CredentialManager:   credMgr,
		ChannelRegistry:     registry,
		RuntimeState:        runtimeState,
		OnChannelAdded:      onChannelAdded,
		OnChannelChange:     onChannelChange,
		ActivityTracker:     activityTracker,
		ActiveChannel:       activeChannelFunc,
		Links:               linksFunc,
		CrossChOutput:       chan<- channel.TaggedMessage(crossChannelMsgs),
		CrossChSend:         messageOutbox.Send,
		ChannelsFunc:        channelsFunc,
		SessionStore:        sessionStore,
		HomeDir:             homeDir,
		MemoryDir:           memoryDir,
		ScheduleStore:       scheduleStore,
		Scheduler:           scheduler,
		NotificationManager: notificationManager,
		DevStore:            devStore,
		LogBuffer:           r.logBuffer,
		RepoStore:           repoStore,
		RemoteMCPManager:    remoteMCPMgr,
		ConfigUpdater:       configUpdater,
		BaseURL:             secretFormBaseURL,
		RegisterHandler:     secretFormRegisterHandler,
		OnboardingStore:     onboardingStore,
		ModelStore:          s,
		TelegramUserID:      mu.cfg.TelegramUserID,
	})

	regCtx := toolpkg.RegistrationContext{
		SecretStore:       secretStore,
		StateStore:        s,
		Callback:          r.callback,
		CredentialManager: credMgr,
		Registry:          toolRegistry,
		MemoryDir:         memoryDir,
	}

	// Seed pre-provisioned secrets from env vars. Packages declare their own
	// secrets via RequiredSecrets() — the registry iterates all of them.
	if err := toolRegistry.SeedAllSecrets(ctx, string(mu.cfg.ID), secretStore); err != nil {
		slog.Error("failed to seed secrets from env", "user", mu.cfg.ID, "err", err)
	}

	// Register all tool packages via the registry. This calls Register() on
	// each package and auto-generates info tools.
	registerCredentialSystem(ctx, mcpHandler, toolRegistry, credMgr, regCtx, string(mu.cfg.ID))

	// Signal that all notifiers are registered so the notification manager
	// can safely resubscribe persisted subscriptions.
	close(notifiersReady)

	// Populate group tools from packages so channels can resolve tool groups.
	toolgroup.SetPackageTools(toolRegistry.BuildGroupTools())

	// tool_list is registered inside channeltools.Register() — no separate call needed.

	// Ephemeral channel cleanup goroutine. Runs at user lifetime and
	// periodically tears down ephemeral channels that have been idle past
	// their timeout. Reads channel config each tick via the config writer.
	go cleanupEphemeralChannels(ctx, mu.cfg.ID, configWriter, runtimeState, activityTracker, secretStore, provisioners, onChannelChange, messageQueue, channelSet.Snapshot, devStore)

	// knowledgeSyncMu serializes background vault syncs so an OnTurnEnd firing
	// before the previous sync finished can't run concurrent git commands
	// against the same clone. Lives at user lifetime, shared across agent
	// restarts within this session.
	var knowledgeSyncMu sync.Mutex

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

		// Re-seed the knowledge skill each iteration (idempotent overwrite) so a
		// reset that cleared home/.claude/ is repaired before the next agent spawn.
		if mu.cfg.Knowledge != nil {
			seedKnowledgeSkill(mu.cfg.ID, homeDir, dirs.Knowledge)
		}

		// Re-seed the Google Workspace CLI skills each iteration (idempotent
		// overwrite) so the agent discovers gws command syntax from skills before
		// the next agent spawn.
		seedGWSSkills(mu.cfg.ID, homeDir)

		// Regenerate the MCP config on each iteration so the file is always
		// present before the next agent spawn.
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
			RuntimeState: runtimeState,
			Provisioners: provisioners,
		})
		if reconcileErr != nil {
			slog.Error("channel reconciliation failed", "user", mu.cfg.ID, "err", reconcileErr)
		}

		// Build transports only for ready channels. Notify parents of provisioning failures.
		var readyChannels []config.Channel
		for _, rc := range reconciled {
			if rc.Status == reconciler.ChannelReady {
				readyChannels = append(readyChannels, rc.Config)
				continue
			}
			if rc.ProvisionErr != nil {
				notifyParent(dynamicCtx, notifyParentParams{
					ChildName:    rc.Config.Name,
					Parent:       rc.Config.Parent,
					Message:      fmt.Sprintf("Channel %q provisioning failed: %v", rc.Config.Name, rc.ProvisionErr),
					Queue:        messageQueue,
					ChannelsFunc: channelSet.Snapshot,
				})
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

		liveChannels, buildFailures := r.BuildChannels(dynamicCtx, BuildChannelsParams{
			UserID:      mu.cfg.ID,
			UserCfg:     currentUserCfg,
			Channels:    readyChannels,
			Env:         r.env,
			StateStore:  s,
			SecretStore: secretStore,
		})
		for _, bf := range buildFailures {
			notifyParent(dynamicCtx, notifyParentParams{
				ChildName:    bf.Name,
				Parent:       bf.Parent,
				Message:      fmt.Sprintf("Channel %q failed to start: %v", bf.Name, bf.Err),
				Queue:        messageQueue,
				ChannelsFunc: channelSet.Snapshot,
			})
		}
		allChMap := channel.ChannelMap(liveChannels...)
		// Also include the initial channels so existing listeners are reachable.
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

		// Merge message streams. mergeAgentInputs (not a plain fan-in) requeues a
		// non-user message that's in flight when this iteration is cancelled, so a
		// schedule firing at a restart boundary isn't silently dropped.
		mergedMsgs := mergeAgentInputs(dynamicCtx, messageQueue, allStaticMsgs, dynamicMsgs)

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
		knowledgeDir := dirs.Knowledge

		// Load persisted queue state (queued messages + interrupted marker)
		// before checking auto-resume. Without this, checkAutoResume sees an
		// empty queue on first boot and misses the persisted interrupted marker.
		if loadErr := messageQueue.LoadPersisted(ctx); loadErr != nil {
			slog.Error("failed to load persisted queue", "err", loadErr)
		}

		// Decide whether we already have enough reason to start the agent
		// (resume marker or queued work waiting) or should block for a fresh
		// live message. See determineStartupSignal for the rules.
		decision := determineStartupSignal(ctx, messageQueue, allChMap)
		var firstMsg channel.TaggedMessage
		if decision.FirstMessage != nil {
			firstMsg = *decision.FirstMessage
		}

		// StartNow covers both resume and persisted-queue cases. Only block
		// on live inbound messages when there's literally nothing to do.
		if !decision.StartNow {
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
		}

		if firstMsg.ChannelID == "" {
			// Started for persisted queue work without a synthetic first
			// message — the agent will call Queue.Next() which dequeues the
			// persisted messages before reading from bridgeCh.
			slog.Info("starting agent to drain persisted queue", "user", mu.cfg.ID, "queued", messageQueue.Len())
		} else {
			slog.Info("message received, starting agent", "user", mu.cfg.ID, "channel", firstMsg.ChannelID)
			if ch, ok := allChMap[firstMsg.ChannelID]; ok {
				source := channel.SourceUser
				if firstMsg.SourceInfo != nil {
					source = firstMsg.SourceInfo.Source
				}
				activityTracker.MessageReceivedFrom(ch.Info().Name, source)
			}
		}

		// On restarts (idle timeout, deploy, channel change), pass a resume
		// notice via Options so the CLI sees the warning while builtin
		// command detection still operates on the raw user input.
		var resumeNotice string
		if isRestart {
			resumeNotice = "[SYSTEM: Session resumed after restart. " +
				"Treat all prior conversation as read-only context. " +
				"Do NOT re-execute or continue any actions from before the restart — " +
				"short replies like \"ya\" or \"yes\" in the history are NOT authorization " +
				"for pending actions. Wait for explicit new instructions.]\n"
		}

		agentCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})

		r.mu.Lock()
		mu.cancel = cancel
		mu.done = done
		r.mu.Unlock()

		// Remove stale CLI session files and old session store records
		// to keep the projects directory lean and CLI startup fast.
		cleanupStaleSessions(ctx, sessionStore, stores.Session, dirs.Sessions, homeDir)

		// Map channels with a configured claude_session_timeout to their duration.
		// Empty/zero means "no timeout — session lives until explicit reset".
		claudeSessionTimeouts := make(map[channel.ChannelID]time.Duration, len(currentUserCfg.Channels))
		for _, chCfg := range currentUserCfg.Channels {
			if chCfg.ClaudeSessionTimeout == "" {
				continue
			}
			timeout, parseErr := time.ParseDuration(chCfg.ClaudeSessionTimeout)
			if parseErr != nil {
				// Config validation should have caught this — log and skip.
				slog.Error("invalid claude_session_timeout, ignoring",
					"channel", chCfg.Name, "value", chCfg.ClaudeSessionTimeout, "err", parseErr)
				continue
			}
			for chID, ch := range allChMap {
				if ch.Info().Name == chCfg.Name {
					claudeSessionTimeouts[chID] = timeout
					break
				}
			}
		}

		// Load sessions from store for each channel, respecting the per-channel
		// claude_session_timeout. The Sessions map is the fallback path when
		// SessionResolver isn't consulted (tests); the live resolver below is
		// what governs production behaviour.
		sessions := make(map[channel.ChannelID]string)
		for chID := range allChMap {
			timeout := claudeSessionTimeouts[chID]
			sid, loadErr := sessionStore.CurrentWithin(ctx, channel.SessionKey(chID), timeout)
			if loadErr != nil {
				slog.Warn("failed to load session, starting fresh", "channel", chID, "err", loadErr)
			}
			if sid != "" {
				sessions[chID] = sid
			}
		}

		// Bridge: re-emit the first message (if any) plus remaining merged
		// messages into a channel that agent.RunWithMessages reads from.
		// When started to drain persisted queue work, firstMsg is empty and
		// we skip the pre-fill — the agent's Queue.Next() dequeues the
		// persisted messages directly before reading from bridgeCh.
		bridgeCh := make(chan channel.TaggedMessage, 1)
		if firstMsg.ChannelID != "" {
			bridgeCh <- firstMsg
		}

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
					// Record message arrival for activity tracking so IsBusy
					// returns true as soon as the message enters the pipeline.
					// Only track user/resume messages here — non-user messages
					// (schedule, cross-channel, notification) may be queued and
					// processed later. Tracking them on arrival would reset the
					// target channel's cooldown timer, blocking the message behind
					// its own arrival. Non-user messages get tracked when they
					// actually start processing (via OnTurnStart below).
					if ch := channelSet.Lookup(msg.ChannelID); ch != nil {
						source := channel.SourceUser
						if msg.SourceInfo != nil {
							source = msg.SourceInfo.Source
						}
						if source == channel.SourceUser || source == channel.SourceResume {
							activityTracker.MessageReceivedFrom(ch.Info().Name, source)
						}
					}
					// Intercept messages answering a confirmation the agent asked
					// for (channel teardown, a repo access grant). Handled here
					// rather than passed to the agent, so the agent can never
					// answer its own prompt.
					// Confirmation outcomes are reported straight to the chat via
					// the outbox: the agent isn't running this turn, so there is
					// nothing else to tell the user what happened.
					notifyChannel := func(ctx context.Context, chID channel.ChannelID, text string) {
						if _, sendErr := messageOutbox.Send(ctx, chID, text, channel.SendOpts{}); sendErr != nil {
							slog.Error("failed to report confirmation outcome", "channel", chID, "err", sendErr)
						}
					}
					if interceptPendingConfirmation(agentCtx, msg, confirmParams{
						ChannelsFunc:    channelsFunc,
						RuntimeState:    runtimeState,
						ConfigWriter:    configWriter,
						UserID:          mu.cfg.ID,
						SecretStore:     secretStore,
						Provisioners:    provisioners,
						RepoStore:       repoStore,
						Notify:          notifyChannel,
						OnChannelChange: onChannelChange,
						MemoryDir:       memoryDir,
					}) {
						continue
					}
					select {
					case bridgeCh <- msg:
					case <-agentCtx.Done():
						// Same guard as mergeAgentInputs, one hop later: a non-user
						// message pulled here but not handed to the agent before the
						// turn ends must survive to the next start, not be dropped.
						if msg.SourceInfo != nil && msg.SourceInfo.Source != channel.SourceUser {
							if err := messageQueue.Push(context.WithoutCancel(agentCtx), msg); err != nil {
								slog.Error("router: failed to requeue non-user message on agent shutdown",
									"channel", msg.ChannelID, "err", err)
							}
						}
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

		// Per-channel model overrides, keyed by channel ID (empty for channels
		// that inherit the user-level model).
		channelModels := buildChannelModels(allChMap, registry)

		// Generate per-channel MCP config files for channels with scoped remote MCPs.
		mcpConfigPaths := buildMCPConfigPaths(dynamicCtx, allChMap, remoteMCPMgr, remoteMCPProxy, proxyToken, mcpConfigDir, mcpAddr, mcpToken)

		// Start the outbox for this iteration so delivery goroutines use
		// the current channel set. Stop on iteration exit to persist state.
		messageOutbox.Start(dynamicCtx)

		// channelChangeNotify is closed when a channel change fires,
		// telling the agent to finish its current turn then exit.
		// Created per iteration because a closed channel can't be reused.
		channelChangeNotify := make(chan struct{})

		opts := agent.Options{
			PermissionMode: mu.cfg.PermissionMode,
			Model:          mu.cfg.Model,
			ModelFunc: func() claudecli.Model {
				// Raw runtime override (empty when unset) so the agent can layer
				// per-channel models beneath it. See resolveModelForChannel.
				return modeltools.LoadOverride(s)
			},
			ChannelModels: channelModels,
			MaxTurns:      mu.cfg.MaxTurns,
			Debug:         mu.cfg.Debug,
			APIKey:        mu.cfg.APIKey,
			HomeDir:       homeDir,
			MemoryDir:     memoryDir,
			AddDirs:       addDirs,
			AddDirsFunc: func(chID channel.ChannelID) []string {
				// Read from the dev store each turn so worktrees created
				// mid-session (via dev_start) are immediately accessible.
				// Always include the parent worktrees dir so bwrap can bind it.
				// This replaces the static AddDirs (see handle.go), so it must
				// also carry the knowledge-base clone when it's provisioned.
				dirs := []string{worktreesDir}
				if _, statErr := os.Stat(knowledgeDir); statErr == nil {
					dirs = append(dirs, knowledgeDir)
				}
				// Only the repos this channel may see — the parent repos dir is
				// bound separately via SandboxDirs, so the --add-dir list is
				// what scopes the CLI's own file access.
				dirs = append(dirs, resolveRepoMounts(ctx, repoStore, channelName(channelsFunc(), chID)).Visible...)
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
			// Bind the repos parent so a repo cloned mid-turn (repo_add +
			// repo_sync) is readable straight away — binds are fixed when the
			// subprocess spawns, so a clone created afterwards is only visible
			// through an already-bound parent.
			SandboxDirs: func(_ channel.ChannelID) []string {
				return []string{reposDir}
			},
			ReadOnlyDirs: func(chID channel.ChannelID) []string {
				// Repo clones are mirrors — repo_sync resets them to the remote,
				// so any edit the agent made would vanish without warning.
				return resolveRepoMounts(ctx, repoStore, channelName(channelsFunc(), chID)).Visible
			},
			MaskedDirs: func(chID channel.ChannelID) []string {
				return resolveRepoMounts(ctx, repoStore, channelName(channelsFunc(), chID)).Masked
			},
			Channels: allChMap,
			// ChannelsFunc provides live channel lookups so hot-added channels
			// are reachable by the agent without restarting.
			ChannelsFunc: channelsFunc,
			Sessions:     sessions,
			SessionResolver: func(chID channel.ChannelID) (string, bool) {
				timeout := claudeSessionTimeouts[chID]
				sid, timedOut, err := sessionStore.CurrentWithinDetailed(ctx, channel.SessionKey(chID), timeout)
				if err != nil {
					slog.Warn("failed to resolve session, starting fresh", "channel", chID, "err", err)
					return "", false
				}
				return sid, timedOut
			},
			Queue:  messageQueue,
			Outbox: messageOutbox,
			OnSessionUpdate: func(chID channel.ChannelID, sessionID string) {
				if saveErr := sessionStore.SetCurrent(ctx, channel.SessionKey(chID), sessionID); saveErr != nil {
					slog.Error("failed to save session", "err", saveErr)
				}
			},
			OnTurnStart: func(channelName string) {
				activeChannelName.Store(&channelName)
				activityTracker.MessageReceived(channelName)
				activityTracker.TurnStarted(channelName)
			},
			OnTurnEnd: func(channelName string) {
				activityTracker.TurnEnded(channelName)

				if kc := mu.cfg.Knowledge; kc != nil {
					// Background so the turn (and the next one) is never blocked on
					// git/network I/O. Bound to the router's own ctx (not the
					// per-turn ctx, which is already cancelled by the time this
					// fires) so the sync can outlive the turn but still stops at
					// shutdown.
					go func() {
						knowledgeSyncMu.Lock()
						defer knowledgeSyncMu.Unlock()
						syncKnowledgeVault(ctx, knowledgeSyncParams{
							Dir:          knowledgeDir,
							UserID:       string(mu.cfg.ID),
							ChannelName:  channelName,
							Outbox:       messageOutbox,
							ChannelsFunc: channelsFunc,
						})
					}()
				}
			},
			AllowedTools:         mu.cfg.AllowedTools,
			DisallowedTools:      mu.cfg.DisallowedTools,
			ChannelToolOverrides: channelToolOverrides,
			MCPConfigPath:        mcpConfigPath,
			MCPConfigPaths:       mcpConfigPaths,
			// Live query so globs expand against tools registered mid-session
			// (local MCP tools added after OAuth, remote MCPs added via
			// remote_mcp_add). Returns FULLY-QUALIFIED names
			// (mcp__<server>__<tool>) so expandMCPGlobs can use them
			// directly without having to know the local-vs-remote split.
			MCPToolNames: func() []string {
				tools := mcpHandler.ListTools()
				names := make([]string, 0, len(tools))
				for _, td := range tools {
					names = append(names, "mcp__tclaw__"+td.Name)
				}
				mcps, err := remoteMCPMgr.ListRemoteMCPs(dynamicCtx)
				if err != nil {
					slog.Error("failed to list remote mcps for tool-name expansion", "err", err)
					return names
				}
				for _, m := range mcps {
					for _, tool := range m.ToolNames {
						names = append(names, "mcp__"+m.Name+"__"+tool)
					}
				}
				return names
			},
			SystemPrompt:    systemPrompt,
			SecretStore:     secretStore,
			ChannelChangeCh: channelChangeNotify,
			Env:             r.env,
			UserID:          string(mu.cfg.ID),
			SetupToken:      setupToken,
			HasProdConfig:   config.HasEnv(r.configPath, config.EnvProd),
			ResumeNotice:    resumeNotice,
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
			case <-time.After(2 * time.Minute):
				// Safety timeout — force cancel if the turn is stuck.
				slog.Warn("agent did not exit after channel change, forcing", "user", mu.cfg.ID)
				cancel()
				err = <-agentErr
			}
		}

		cancel()
		<-done
		<-bridgeDone

		// Cancel agent-created channels so their listeners/sockets close.
		// Next iteration will recreate them from the (possibly updated) config.
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
		if u.registry != nil && u.channelSet != nil {
			snapshot := u.channelSet.Snapshot()
			channels := make([]channel.Channel, 0, len(snapshot))
			for _, ch := range snapshot {
				channels = append(channels, ch)
			}
			sendLifecycleNotification(shutdownCtx, channels, u.registry, msg)
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
		if _, sendErr := ch.Send(ctx, message, channel.SendOpts{}); sendErr != nil {
			slog.Warn("failed to send lifecycle notification", "channel", ch.Info().Name, "err", sendErr)
		}
	}
}

// BuildChannels creates channel instances from config for a given user.
// Dispatches to the channel registry by type.
// Channels whose Envs list doesn't include env are skipped.
type BuildChannelsParams struct {
	UserID      user.ID
	UserCfg     config.User
	Channels    []config.Channel
	Env         config.Env
	StateStore  store.Store
	SecretStore secret.Store
}

// BuildFailure records a channel that failed to build on startup.
type BuildFailure struct {
	Name   string
	Parent string
	Err    error
}

func (r *Router) BuildChannels(ctx context.Context, params BuildChannelsParams) ([]channel.Channel, []BuildFailure) {
	var channels []channel.Channel
	var failures []BuildFailure
	for _, chCfg := range params.Channels {
		if len(chCfg.Envs) > 0 && !slices.Contains(chCfg.Envs, params.Env) {
			slog.Info("skipping channel (env mismatch)", "channel", chCfg.Name, "envs", chCfg.Envs, "current", params.Env)
			continue
		}

		var registerHandler func(string, http.Handler)
		if r.callback != nil {
			registerHandler = r.callback.Handle
		}

		ch, err := r.channelRegistry.Build(ctx, chCfg.Type, channelpkg.BuildParams{
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
			// Skip channels that fail to build so one broken channel doesn't
			// take down the entire app. The agent can fix or delete the channel.
			slog.Error("skipping channel (build failed)", "channel", chCfg.Name, "err", err)
			failures = append(failures, BuildFailure{
				Name:   chCfg.Name,
				Parent: chCfg.Parent,
				Err:    err,
			})
			continue
		}
		channels = append(channels, ch)
	}
	return channels, failures
}
