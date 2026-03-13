package router

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"path/filepath"

	"tclaw/channel"
	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/provider"
	"tclaw/libraries/secret"
	googletools "tclaw/tool/google"
	monzotools "tclaw/tool/monzo"
	"tclaw/user"
)

// buildDynamicChannels loads dynamic channel configs from the store and creates
// SocketServer instances for each. Returns a channel map and a fan-in of messages.
// The caller should cancel dynamicCtx when the agent exits to close the listeners.
func (r *Router) buildDynamicChannels(dynamicCtx context.Context, userID user.ID, dynamicStore *channel.DynamicStore, secretStore secret.Store) (map[channel.ChannelID]channel.Channel, <-chan channel.TaggedMessage) {
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
		switch cfg.Type {
		case channel.TypeSocket:
			if !r.env.IsLocal() {
				slog.Info("skipping dynamic socket channel (non-local env)", "channel", cfg.Name, "env", r.env)
				continue
			}
			socketPath := filepath.Join(r.baseDir, string(userID), cfg.Name+".sock")
			sock := channel.NewDynamicSocketServer(socketPath, cfg.Name, cfg.Description)
			channels[sock.Info().ID] = sock
		case channel.TypeTelegram:
			token, tokenErr := secretStore.Get(dynamicCtx, channel.ChannelSecretKey(cfg.Name))
			if tokenErr != nil {
				slog.Error("failed to read telegram bot token from secret store", "channel", cfg.Name, "err", tokenErr)
				continue
			}
			if token == "" {
				slog.Error("telegram bot token not found in secret store", "channel", cfg.Name)
				continue
			}

			var opts channel.TelegramOptions
			if r.publicURL != "" && r.callback != nil {
				webhookSecret := make([]byte, 16)
				if _, err := rand.Read(webhookSecret); err != nil {
					slog.Error("failed to generate webhook path", "channel", cfg.Name, "err", err)
					continue
				}
				webhookPath := "/telegram/" + hex.EncodeToString(webhookSecret)
				opts.WebhookURL = r.publicURL + webhookPath
				opts.RegisterHandler = func(pattern string, handler http.Handler) {
					r.callback.Handle(pattern, handler)
				}
			}
			tg := channel.NewDynamicTelegram(token, cfg.Name, cfg.Description, cfg.AllowedUsers, opts)
			channels[tg.Info().ID] = tg
		default:
			slog.Warn("skipping dynamic channel with unsupported type", "channel", cfg.Name, "type", cfg.Type)
		}
	}

	slog.Info("built dynamic channels", "user", userID, "count", len(channels))
	return channels, channel.FanIn(dynamicCtx, channels)
}

// registerProviderTools loads existing connections and registers
// provider-specific MCP tools for connections that already have credentials stored.
func (r *Router) registerProviderTools(ctx context.Context, h *mcp.Handler, mgr *connection.Manager, googleConns map[connection.ConnectionID]googletools.Deps, monzoConns map[connection.ConnectionID]monzotools.Deps) {
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

		r.registerToolsForProvider(h, conn.ID, mgr, p, googleConns, monzoConns)
	}
}

// registerToolsForProvider adds a connection to the provider's tool set
// and re-registers tools with the updated connection list.
func (r *Router) registerToolsForProvider(h *mcp.Handler, connID connection.ConnectionID, mgr *connection.Manager, p *provider.Provider, googleConns map[connection.ConnectionID]googletools.Deps, monzoConns map[connection.ConnectionID]monzotools.Deps) {
	switch p.ID {
	case provider.GoogleProviderID:
		googleConns[connID] = googletools.Deps{
			ConnID:   connID,
			Manager:  mgr,
			Provider: p,
		}
		googletools.RegisterTools(h, googleConns)
		slog.Info("registered google workspace tools", "connection", connID, "total_connections", len(googleConns))
	case provider.MonzoProviderID:
		monzoConns[connID] = monzotools.Deps{
			ConnID:   connID,
			Manager:  mgr,
			Provider: p,
		}
		monzotools.RegisterTools(h, monzoConns)
		slog.Info("registered monzo tools", "connection", connID, "total_connections", len(monzoConns))
	}
}

// unregisterToolsForProvider removes a connection from the provider's tool set.
// If no connections remain, the tools are removed entirely.
func (r *Router) unregisterToolsForProvider(h *mcp.Handler, connID connection.ConnectionID, googleConns map[connection.ConnectionID]googletools.Deps, monzoConns map[connection.ConnectionID]monzotools.Deps) {
	// Try Google first.
	if _, ok := googleConns[connID]; ok {
		delete(googleConns, connID)
		if len(googleConns) == 0 {
			googletools.UnregisterTools(h)
			slog.Info("unregistered google workspace tools (no connections remain)")
		} else {
			googletools.RegisterTools(h, googleConns)
			slog.Info("updated google workspace tools after disconnect", "removed", connID, "remaining", len(googleConns))
		}
		return
	}

	// Try Monzo.
	if _, ok := monzoConns[connID]; ok {
		delete(monzoConns, connID)
		if len(monzoConns) == 0 {
			monzotools.UnregisterTools(h)
			slog.Info("unregistered monzo tools (no connections remain)")
		} else {
			monzotools.RegisterTools(h, monzoConns)
			slog.Info("updated monzo tools after disconnect", "removed", connID, "remaining", len(monzoConns))
		}
		return
	}
}
