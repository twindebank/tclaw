package router

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"tclaw/agent"
	"tclaw/channel"
	"tclaw/config"
	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/provider"
	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/tool/connectiontools"
	googletools "tclaw/tool/google"
	"tclaw/tool/remotemcp"
	"tclaw/user"
)

// Router manages per-user agent goroutines, each with their own
// channels, store, and Claude session. Agents start lazily on
// the first message, not at registration time.
type Router struct {
	mu       sync.Mutex
	users    map[user.ID]*managedUser
	baseDir  string // root for per-user data (home dirs, stores)
	registry *provider.Registry
	callback *oauth.CallbackServer // nil if OAuth is not configured
	gwsPath  string                // path to gws CLI binary for Google Workspace tools

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
// gets a subdirectory containing their home dir and store.
//
//	baseDir/
//	  <user-id>/
//	    home/       -> HOME for claude subprocess (~/.claude/ lives here)
//	    store/      -> key-value store for agent state (session IDs, etc.)
//	    secrets/    -> encrypted credentials for connections
// callback may be nil if OAuth is not configured.
func New(baseDir string, registry *provider.Registry, callback *oauth.CallbackServer, gwsPath string) *Router {
	return &Router{
		users:      make(map[user.ID]*managedUser),
		mcpServers: make(map[user.ID]*mcp.Server),
		baseDir:    baseDir,
		registry:   registry,
		callback:   callback,
		gwsPath:    gwsPath,
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

	// Start listening on all channels and fan messages into a trigger
	// that starts the agent on the first arrival.
	chMap := channel.ChannelMap(channels...)
	msgs := channel.FanIn(ctx, chMap)

	go r.waitAndStart(ctx, mu, chMap, msgs)

	slog.Info("user registered (agent will start on first message)", "user", cfg.ID)
	return nil
}

// waitAndStart blocks until the first message arrives, then starts the
// agent. If the agent exits due to idle timeout, it goes back to waiting
// for the next message and restarts the agent — repeating indefinitely
// until ctx is cancelled.
func (r *Router) waitAndStart(ctx context.Context, mu *managedUser, chMap map[channel.ChannelID]channel.Channel, msgs <-chan channel.TaggedMessage) {
	userDir := filepath.Join(r.baseDir, string(mu.cfg.ID))
	homeDir := filepath.Join(userDir, "home")
	storeDir := filepath.Join(userDir, "store")
	secretsDir := filepath.Join(userDir, "secrets")

	s, err := store.NewFS(storeDir)
	if err != nil {
		slog.Error("failed to create store", "user", mu.cfg.ID, "err", err)
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
		_, genErr := mcp.GenerateConfigFile(userDir, mcpAddr, remotes)
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
	mcpConfigPath, err := mcp.GenerateConfigFile(userDir, mcpAddr, remotes)
	if err != nil {
		slog.Error("failed to generate mcp config", "user", mu.cfg.ID, "err", err)
		return
	}
	slog.Info("mcp config ready", "user", mu.cfg.ID, "addr", mcpAddr, "config", mcpConfigPath, "remotes", len(remotes))

	// Seed the user's CLAUDE.md memory file if it doesn't exist yet.
	// The Claude CLI auto-loads ~/.claude/CLAUDE.md as global instructions.
	claudeDir := filepath.Join(homeDir, ".claude")
	claudeMDPath := filepath.Join(claudeDir, "CLAUDE.md")
	if _, statErr := os.Stat(claudeMDPath); os.IsNotExist(statErr) {
		if mkErr := os.MkdirAll(claudeDir, 0o700); mkErr != nil {
			slog.Error("failed to create .claude dir", "user", mu.cfg.ID, "err", mkErr)
		} else if wErr := os.WriteFile(claudeMDPath, []byte(agent.DefaultMemoryTemplate), 0o600); wErr != nil {
			slog.Error("failed to seed CLAUDE.md", "user", mu.cfg.ID, "err", wErr)
		} else {
			slog.Info("seeded CLAUDE.md memory file", "user", mu.cfg.ID, "path", claudeMDPath)
		}
	}

	// Convert channel.Info → agent.ChannelInfo for the system prompt.
	var chInfos []agent.ChannelInfo
	for _, ch := range chMap {
		info := ch.Info()
		chInfos = append(chInfos, agent.ChannelInfo{
			Name:        info.Name,
			Type:        string(info.Type),
			Description: info.Description,
		})
	}

	// Build the system prompt once — it's static for this user's lifecycle.
	// Channel list and identity don't change; per-turn current-channel
	// info is prepended to each prompt in handle().
	systemPrompt := agent.BuildSystemPrompt(chInfos, mu.cfg.SystemPrompt)

	for {
		// Wait for a message or shutdown.
		var firstMsg channel.TaggedMessage
		select {
		case <-ctx.Done():
			return
		case m, ok := <-msgs:
			if !ok {
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
		for chID := range chMap {
			sid, loadErr := loadSession(ctx, s, chID)
			if loadErr != nil {
				slog.Warn("failed to load session, starting fresh", "channel", chID, "err", loadErr)
			}
			if sid != "" {
				sessions[chID] = sid
			}
		}

		// Bridge: re-emit the first message plus remaining fan-in messages
		// into a channel that agent.RunWithMessages reads from.
		bridgeCh := make(chan channel.TaggedMessage, 1)
		bridgeCh <- firstMsg

		go func() {
			defer close(bridgeCh)
			for msg := range msgs {
				select {
				case bridgeCh <- msg:
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
			Channels:        chMap,
			Sessions:        sessions,
			OnSessionUpdate: func(chID channel.ChannelID, sessionID string) {
				if saveErr := saveSession(ctx, s, chID, sessionID); saveErr != nil {
					slog.Error("failed to save session", "err", saveErr)
				}
			},
			AllowedTools:    mu.cfg.AllowedTools,
			DisallowedTools: mu.cfg.DisallowedTools,
			MCPConfigPath:   mcpConfigPath,
			SystemPrompt:    systemPrompt,
		}

		agentErr := make(chan error, 1)
		go func() {
			defer close(done)
			agentErr <- agent.RunWithMessages(agentCtx, opts, bridgeCh)
		}()

		// Wait for agent to finish.
		err := <-agentErr
		cancel()
		<-done

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
			GWSPath:  r.gwsPath,
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
// System-derived paths (socket paths) are computed from the base directory.
func (r *Router) BuildChannels(userID user.ID, channelConfigs []config.Channel) ([]channel.Channel, error) {
	var channels []channel.Channel
	for i, chCfg := range channelConfigs {
		switch chCfg.Type {
		case config.ChannelTypeSocket:
			// Each socket channel gets its own path: <base_dir>/<user_id>/<name>.sock
			socketPath := filepath.Join(r.baseDir, string(userID), chCfg.Name+".sock")
			channels = append(channels, channel.NewSocketServer(socketPath, chCfg.Name, chCfg.Description))
		case config.ChannelTypeStdio:
			channels = append(channels, channel.NewStdio())
		default:
			return nil, fmt.Errorf("channel %d: unsupported type %q", i, chCfg.Type)
		}
	}
	return channels, nil
}
