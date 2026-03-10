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

	"tclaw/agent"
	"tclaw/channel"
	"tclaw/config"
	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/provider"
	"tclaw/schedule"
	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/tool/channeltools"
	"tclaw/tool/connectiontools"
	googletools "tclaw/tool/google"
	"tclaw/tool/remotemcp"
	"tclaw/tool/scheduletools"
	"tclaw/user"
)

// Router manages per-user agent goroutines, each with their own
// channels, store, and Claude session. Agents start lazily on
// the first message, not at registration time.
type Router struct {
	mu        sync.Mutex
	users     map[user.ID]*managedUser
	baseDir   string // root for per-user data (home dirs, stores)
	env       string // runtime environment ("local", "prod")
	registry  *provider.Registry
	callback  *oauth.CallbackServer // nil if OAuth is not configured
	publicURL string                // externally-reachable base URL, enables Telegram webhooks

	// Per-user MCP servers, keyed by user ID.
	mcpServers map[user.ID]*mcp.Server
}

type managedUser struct {
	cfg      user.Config
	channels []channel.Channel

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
//	    state/                 -> tclaw persistent data (connections, remote MCPs, channels)
//	    sessions/              -> Claude CLI session IDs per channel
//	    secrets/               -> encrypted credentials (NaCl secretbox)
//	    runtime/               -> ephemeral generated files (mcp-config.json)
//	    *.sock                 -> unix socket files for channels
//
// Zone 1 (memory/): agent reads/writes freely, sandboxed via CWD + --add-dir.
// Zone 2 (home/.claude/): Claude Code internal state, off limits to agent.
// Zone 3 (state/, sessions/, secrets/, runtime/): tclaw data, tool-only access via MCP.
//
// callback may be nil if OAuth is not configured.
func New(baseDir string, env string, registry *provider.Registry, callback *oauth.CallbackServer, publicURL string) *Router {
	return &Router{
		users:      make(map[user.ID]*managedUser),
		mcpServers: make(map[user.ID]*mcp.Server),
		baseDir:    baseDir,
		env:        env,
		registry:   registry,
		callback:   callback,
		publicURL:  publicURL,
	}
}

// Register adds a user and its channels to the router without starting
// the agent. Channels begin listening immediately (sockets accept
// connections) but the agent goroutine starts lazily on the first message.
func (r *Router) Register(ctx context.Context, cfg user.Config, channels []channel.Channel) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.users[cfg.ID]; exists {
		return fmt.Errorf("user %q already registered", cfg.ID)
	}

	mu := &managedUser{
		cfg:      cfg,
		channels: channels,
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
	homeDir := filepath.Join(userDir, "home")       // Claude Code's territory (HOME env var)
	memoryDir := filepath.Join(userDir, "memory")    // agent's sandboxed memory (CWD + --add-dir)
	stateDir := filepath.Join(userDir, "state")      // tclaw persistent data
	sessionsDir := filepath.Join(userDir, "sessions") // Claude CLI session IDs per channel
	secretsDir := filepath.Join(userDir, "secrets")   // encrypted credentials
	runtimeDir := filepath.Join(userDir, "runtime")   // ephemeral generated files

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
	connectiontools.RegisterTools(mcpHandler, connectiontools.Deps{
		Manager:  connMgr,
		Registry: r.registry,
		Callback: r.callback,
		Handler:  mcpHandler,
		OnProviderConnect: func(connID connection.ConnectionID, mgr *connection.Manager, p *provider.Provider) {
			r.registerToolsForProvider(mcpHandler, connID, mgr, p)
		},
	})
	if r.callback != nil {
		connectiontools.RegisterAuthWaitTool(mcpHandler, connMgr)
	}

	// Register provider-specific tools for existing connections.
	r.registerProviderTools(ctx, mcpHandler, connMgr)

	mcpServer := mcp.NewServer(mcpHandler)
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
		_, genErr := mcp.GenerateConfigFile(runtimeDir, mcpAddr, remotes)
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
	mcpConfigPath, err := mcp.GenerateConfigFile(runtimeDir, mcpAddr, remotes)
	if err != nil {
		slog.Error("failed to generate mcp config", "user", mu.cfg.ID, "err", err)
		return
	}
	slog.Info("mcp config ready", "user", mu.cfg.ID, "addr", mcpAddr, "config", mcpConfigPath, "remotes", len(remotes))

	// Seed the user's memory directory and CLAUDE.md.
	// The real file lives in memory/CLAUDE.md (the agent's sandbox). A symlink
	// at home/.claude/CLAUDE.md points to it so the Claude CLI auto-loads it.
	memoryMDPath := filepath.Join(memoryDir, "CLAUDE.md")
	if _, statErr := os.Stat(memoryMDPath); os.IsNotExist(statErr) {
		if mkErr := os.MkdirAll(memoryDir, 0o700); mkErr != nil {
			slog.Error("failed to create memory dir", "user", mu.cfg.ID, "err", mkErr)
		} else if wErr := os.WriteFile(memoryMDPath, []byte(agent.DefaultMemoryTemplate), 0o600); wErr != nil {
			slog.Error("failed to seed CLAUDE.md", "user", mu.cfg.ID, "err", wErr)
		} else {
			slog.Info("seeded memory/CLAUDE.md", "user", mu.cfg.ID, "path", memoryMDPath)
		}
	}

	// Symlink Library/Keychains into the per-user HOME so macOS Keychain
	// works when claude auth login runs with the overridden HOME.
	// The keychain service looks for ~/Library/Keychains/login.keychain-db.
	realHome, _ := os.UserHomeDir()
	if realHome != "" {
		realKeychains := filepath.Join(realHome, "Library", "Keychains")
		if _, statErr := os.Stat(realKeychains); statErr == nil {
			userKeychains := filepath.Join(homeDir, "Library", "Keychains")
			if _, statErr := os.Lstat(userKeychains); os.IsNotExist(statErr) {
				if mkErr := os.MkdirAll(filepath.Join(homeDir, "Library"), 0o700); mkErr != nil {
					slog.Error("failed to create Library dir", "user", mu.cfg.ID, "err", mkErr)
				} else if linkErr := os.Symlink(realKeychains, userKeychains); linkErr != nil {
					slog.Error("failed to symlink Keychains", "user", mu.cfg.ID, "err", linkErr)
				} else {
					slog.Info("symlinked Keychains", "user", mu.cfg.ID, "target", realKeychains)
				}
			}
		}
	}

	// Restore Keychains symlink if a previous OAuth flow crashed mid-rename.
	hiddenKeychains := filepath.Join(homeDir, "Library", "Keychains.hidden")
	normalKeychains := filepath.Join(homeDir, "Library", "Keychains")
	if _, err := os.Lstat(hiddenKeychains); err == nil {
		if _, err := os.Lstat(normalKeychains); os.IsNotExist(err) {
			if renameErr := os.Rename(hiddenKeychains, normalKeychains); renameErr != nil {
				slog.Error("failed to restore hidden Keychains symlink", "user", mu.cfg.ID, "err", renameErr)
			} else {
				slog.Info("restored Keychains symlink from previous crash", "user", mu.cfg.ID)
			}
		}
	}

	// Pre-provision OAuth credentials from a deployed per-user secret
	// (e.g. Fly.io env var CLAUDE_OAUTH_CREDS_<USER>) so headless agents
	// can use OAuth without an interactive browser flow.
	oauthEnvVar := agent.OAuthCredsEnvVarName(string(mu.cfg.ID))
	if creds := os.Getenv(oauthEnvVar); creds != "" {
		if provErr := agent.ProvisionOAuthCredentials(homeDir, creds); provErr != nil {
			slog.Error("failed to provision OAuth credentials", "user", mu.cfg.ID, "err", provErr)
		}
	}

	// Ensure home/.claude/ exists and symlink CLAUDE.md into it.
	claudeDir := filepath.Join(homeDir, ".claude")
	symlinkPath := filepath.Join(claudeDir, "CLAUDE.md")
	if _, statErr := os.Lstat(symlinkPath); os.IsNotExist(statErr) {
		if mkErr := os.MkdirAll(claudeDir, 0o700); mkErr != nil {
			slog.Error("failed to create .claude dir", "user", mu.cfg.ID, "err", mkErr)
		} else if linkErr := os.Symlink(filepath.Join("..", "..", "memory", "CLAUDE.md"), symlinkPath); linkErr != nil {
			slog.Error("failed to create CLAUDE.md symlink", "user", mu.cfg.ID, "err", linkErr)
		} else {
			slog.Info("created CLAUDE.md symlink", "user", mu.cfg.ID, "link", symlinkPath)
		}
	}

	// Create dynamic channel store and register channel management tools.
	dynamicStore := channel.NewDynamicStore(s)

	// Collect static channel infos for the channel tools (immutable reference).
	staticInfos := channel.InfoAll(staticChMap)
	channeltools.RegisterTools(mcpHandler, channeltools.Deps{
		DynamicStore:   dynamicStore,
		StaticChannels: staticInfos,
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

	// Merge schedule messages into the static stream so they outlive the agent.
	allStaticMsgs := channel.MergeFanIns(ctx, staticMsgs, scheduleMsgs)

	for {
		// Build dynamic channels from the store each iteration so
		// creates/deletes from the previous agent session take effect.
		dynamicCtx, cancelDynamic := context.WithCancel(ctx)
		dynamicChMap, dynamicMsgs := r.buildDynamicChannels(dynamicCtx, mu.cfg.ID, dynamicStore)

		// Merge static + dynamic into a combined view for this iteration.
		allChMap := make(map[channel.ChannelID]channel.Channel, len(staticChMap)+len(dynamicChMap))
		for id, ch := range staticChMap {
			allChMap[id] = ch
		}
		for id, ch := range dynamicChMap {
			allChMap[id] = ch
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
		systemPrompt := agent.BuildSystemPrompt(chInfos, mu.cfg.SystemPrompt)

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

		opts := agent.Options{
			PermissionMode:  mu.cfg.PermissionMode,
			Model:           mu.cfg.Model,
			MaxTurns:        mu.cfg.MaxTurns,
			Debug:           mu.cfg.Debug,
			APIKey:          mu.cfg.APIKey,
			HomeDir:         homeDir,
			MemoryDir:       memoryDir,
			Channels:        allChMap,
			Sessions:        sessions,
			OnSessionUpdate: func(chID channel.ChannelID, sessionID string) {
				if saveErr := saveSession(ctx, sessionStore, chID, sessionID); saveErr != nil {
					slog.Error("failed to save session", "err", saveErr)
				}
			},
			AllowedTools:    mu.cfg.AllowedTools,
			DisallowedTools: mu.cfg.DisallowedTools,
			MCPConfigPath:   mcpConfigPath,
			SystemPrompt:    systemPrompt,
			SecretStore:     secretStore,
			Env:             r.env,
			UserID:          string(mu.cfg.ID),
		}

		agentErr := make(chan error, 1)
		go func() {
			defer close(done)
			agentErr <- agent.RunWithMessages(agentCtx, opts, bridgeCh)
		}()

		// Wait for agent and bridge goroutine to finish.
		// The bridge must exit before we loop back to reading mergedMsgs,
		// otherwise both the bridge and the main loop race to read
		// from the same channel and the bridge drops the message.
		err := <-agentErr
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
		if err != nil {
			slog.Error("agent exited with error", "user", mu.cfg.ID, "err", err)
		}
		return
	}
}

// buildDynamicChannels loads dynamic channel configs from the store and creates
// SocketServer instances for each. Returns a channel map and a fan-in of messages.
// The caller should cancel dynamicCtx when the agent exits to close the listeners.
func (r *Router) buildDynamicChannels(dynamicCtx context.Context, userID user.ID, dynamicStore *channel.DynamicStore) (map[channel.ChannelID]channel.Channel, <-chan channel.TaggedMessage) {
	configs, err := dynamicStore.List(dynamicCtx)
	if err != nil {
		slog.Error("failed to load dynamic channels", "user", userID, "err", err)
		return nil, nil
	}
	if len(configs) == 0 {
		return nil, nil
	}

	channels := make(map[channel.ChannelID]channel.Channel, len(configs))
	for _, cfg := range configs {
		socketPath := filepath.Join(r.baseDir, string(userID), cfg.Name+".sock")
		sock := channel.NewDynamicSocketServer(socketPath, cfg.Name, cfg.Description)
		channels[sock.Info().ID] = sock
	}

	slog.Info("built dynamic channels", "user", userID, "count", len(channels))
	return channels, channel.FanIn(dynamicCtx, channels)
}

// registerProviderTools loads existing connections and registers
// provider-specific MCP tools for connections that already have credentials stored.
func (r *Router) registerProviderTools(ctx context.Context, h *mcp.Handler, mgr *connection.Manager) {
	conns, err := mgr.List(ctx)
	if err != nil {
		slog.Error("failed to list connections for tool registration", "err", err)
		return
	}

	for _, conn := range conns {
		p := r.registry.Get(conn.ProviderID)
		if p == nil {
			continue
		}

		// Only register tools if the connection has valid credentials.
		creds, err := mgr.GetCredentials(ctx, conn.ID)
		if err != nil {
			slog.Warn("failed to check credentials", "connection", conn.ID, "err", err)
			continue
		}
		if creds == nil || creds.AccessToken == "" {
			continue
		}

		r.registerToolsForProvider(h, conn.ID, mgr, p)
	}
}

// registerToolsForProvider registers provider-specific MCP tools for a single connection.
func (r *Router) registerToolsForProvider(h *mcp.Handler, connID connection.ConnectionID, mgr *connection.Manager, p *provider.Provider) {
	switch p.ID {
	case provider.GoogleProviderID:
		googletools.RegisterTools(h, googletools.Deps{
			ConnID:   connID,
			Manager:  mgr,
			Provider: p,
		})
		slog.Info("registered google workspace tools", "connection", connID)
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
	delete(r.users, userID)
	r.mu.Unlock()

	// Agent may not have started yet.
	if u.cancel != nil {
		u.cancel()
		<-u.done
	}
	slog.Info("user stopped", "user", userID)
}

// StopAll cancels all users and waits for them to finish.
func (r *Router) StopAll() {
	r.mu.Lock()
	users := make(map[user.ID]*managedUser, len(r.users))
	maps.Copy(users, r.users)
	r.users = make(map[user.ID]*managedUser)
	r.mu.Unlock()

	for id, u := range users {
		if u.cancel != nil {
			u.cancel()
			<-u.done
		}
		slog.Info("user stopped", "user", id)
	}
}

// BuildChannels creates channel instances from config for a given user.
// Channels whose Envs list doesn't include env are skipped.
// System-derived paths (socket paths) are computed from the base directory.
func (r *Router) BuildChannels(userID user.ID, channelConfigs []config.Channel, env string) ([]channel.Channel, error) {
	var channels []channel.Channel
	for i, chCfg := range channelConfigs {
		if len(chCfg.Envs) > 0 && !slices.Contains(chCfg.Envs, env) {
			slog.Info("skipping channel (env mismatch)", "channel", chCfg.Name, "envs", chCfg.Envs, "current", env)
			continue
		}

		switch chCfg.Type {
		case config.ChannelTypeSocket:
			// Each socket channel gets its own path: <base_dir>/<user_id>/<name>.sock
			socketPath := filepath.Join(r.baseDir, string(userID), chCfg.Name+".sock")
			channels = append(channels, channel.NewSocketServer(socketPath, chCfg.Name, chCfg.Description))
		case config.ChannelTypeStdio:
			channels = append(channels, channel.NewStdio())
		case config.ChannelTypeTelegram:
			var opts channel.TelegramOptions
			if r.publicURL != "" && r.callback != nil {
				webhookPath := "/telegram/" + chCfg.Name
				opts.WebhookURL = r.publicURL + webhookPath
				opts.RegisterHandler = func(pattern string, handler http.Handler) {
					r.callback.Handle(pattern, handler)
				}
			}
			channels = append(channels, channel.NewTelegram(chCfg.Token, chCfg.Name, chCfg.Description, opts))
		default:
			return nil, fmt.Errorf("channel %d: unsupported type %q", i, chCfg.Type)
		}
	}
	return channels, nil
}
