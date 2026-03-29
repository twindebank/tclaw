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
	"tclaw/credential"
	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/provider"
	googletools "tclaw/tool/google"
	monzotools "tclaw/tool/monzo"
	"tclaw/tool/providerutil"
	"tclaw/user"
)

// injectInitialMessages delivers the initial_message for any dynamic channels
// that have one set, then clears the field so it fires exactly once. Must be
// called after buildDynamicChannels so channel IDs are known.
func (r *Router) injectInitialMessages(ctx context.Context, userID user.ID, dynamicStore *channel.DynamicStore, dynamicChMap map[channel.ChannelID]channel.Channel, output chan<- channel.TaggedMessage) {
	configs, err := dynamicStore.List(ctx)
	if err != nil {
		slog.Error("failed to list dynamic channels for initial message injection", "user", userID, "err", err)
		return
	}

	for _, cfg := range configs {
		if cfg.InitialMessage == "" {
			continue
		}

		// Find the ChannelID for this channel name in the current dynamic map.
		var targetID channel.ChannelID
		for id, ch := range dynamicChMap {
			if ch.Info().Name == cfg.Name {
				targetID = id
				break
			}
		}
		if targetID == "" {
			slog.Warn("initial_message: channel not found in dynamic map, skipping", "channel", cfg.Name)
			continue
		}

		// Clear before delivery — if the send below fails we still don't retry,
		// which is preferable to firing the message on every subsequent restart.
		clearErr := dynamicStore.Update(ctx, cfg.Name, func(c *channel.DynamicChannelConfig) {
			c.InitialMessage = ""
		})
		if clearErr != nil {
			slog.Error("failed to clear initial_message, skipping delivery to prevent duplicate fires", "channel", cfg.Name, "err", clearErr)
			continue
		}

		msg := channel.TaggedMessage{
			ChannelID: targetID,
			Text:      cfg.InitialMessage,
		}
		select {
		case output <- msg:
			slog.Info("injected initial_message for channel", "channel", cfg.Name)
		default:
			slog.Warn("initial_message output channel full, dropping message", "channel", cfg.Name)
		}
	}
}

// buildSingleDynamicChannel loads one dynamic channel by name and starts it.
// Returns a single-entry map and its message fan-in, or nil on error. Used for
// hot-adding a newly created channel without restarting the whole agent session.
func (r *Router) buildSingleDynamicChannel(ctx context.Context, userID user.ID, dynamicStore *channel.DynamicStore, secretStore secret.Store, stateStore store.Store, name string) (map[channel.ChannelID]channel.Channel, <-chan channel.TaggedMessage) {
	cfg, err := dynamicStore.Get(ctx, name)
	if err != nil {
		slog.Error("hot-add: failed to load channel config", "channel", name, "user", userID, "err", err)
		return nil, nil
	}
	if cfg == nil {
		slog.Warn("hot-add: channel not found in store", "channel", name, "user", userID)
		return nil, nil
	}

	channels := make(map[channel.ChannelID]channel.Channel, 1)
	switch cfg.Type {
	case channel.TypeSocket:
		if !r.env.IsLocal() {
			slog.Info("hot-add: skipping socket channel (non-local env)", "channel", cfg.Name, "env", r.env)
			return nil, nil
		}
		socketPath := filepath.Join(r.baseDir, string(userID), cfg.Name+".sock")
		sock := channel.NewDynamicSocketServer(socketPath, cfg.Name, cfg.Description)
		channels[sock.Info().ID] = sock
	case channel.TypeTelegram:
		token, tokenErr := secretStore.Get(ctx, channel.ChannelSecretKey(cfg.Name))
		if tokenErr != nil {
			slog.Error("hot-add: failed to read telegram bot token", "channel", cfg.Name, "err", tokenErr)
			return nil, nil
		}
		if token == "" {
			slog.Error("hot-add: telegram bot token not found", "channel", cfg.Name)
			return nil, nil
		}

		var opts channel.TelegramOptions
		if r.publicURL != "" && r.callback != nil {
			webhookSecret := make([]byte, 16)
			if _, webhookErr := rand.Read(webhookSecret); webhookErr != nil {
				slog.Error("hot-add: failed to generate webhook path", "channel", cfg.Name, "err", webhookErr)
				return nil, nil
			}
			webhookPath := "/telegram/" + hex.EncodeToString(webhookSecret)
			opts.WebhookURL = r.publicURL + webhookPath
			opts.WebhookPath = webhookPath
			opts.RegisterHandler = func(pattern string, handler http.Handler) {
				r.callback.Handle(pattern, handler)
			}
		}
		opts.ChatID = loadChatID(ctx, stateStore, cfg.Name)
		if opts.ChatID == 0 {
			// Fall back to the chat ID stored at channel creation time so the
			// bot can send messages before any inbound user message arrives.
			if tps, ok := cfg.PlatformState.(channel.TelegramPlatformState); ok && tps.ChatID != 0 {
				opts.ChatID = tps.ChatID
			}
		}
		opts.OnChatID = saveChatIDFunc(stateStore, cfg.Name)
		opts.MediaDir = filepath.Join(r.baseDir, string(userID), "memory", "media")
		tg := channel.NewDynamicTelegram(token, cfg.Name, cfg.Description, cfg.AllowedUsers, opts)
		channels[tg.Info().ID] = tg
	default:
		slog.Warn("hot-add: unsupported channel type", "channel", cfg.Name, "type", cfg.Type)
		return nil, nil
	}

	slog.Info("hot-added dynamic channel", "channel", name, "user", userID)
	return channels, channel.FanIn(ctx, channels)
}

// buildDynamicChannels loads dynamic channel configs from the store and creates
// SocketServer instances for each. Returns a channel map and a fan-in of messages.
// The caller should cancel dynamicCtx when the agent exits to close the listeners.
func (r *Router) buildDynamicChannels(dynamicCtx context.Context, userID user.ID, dynamicStore *channel.DynamicStore, secretStore secret.Store, stateStore store.Store) (map[channel.ChannelID]channel.Channel, <-chan channel.TaggedMessage) {
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
				slog.Debug("skipping dynamic socket channel (non-local env)", "channel", cfg.Name, "env", r.env)
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
				opts.WebhookPath = webhookPath
				opts.RegisterHandler = func(pattern string, handler http.Handler) {
					r.callback.Handle(pattern, handler)
				}
			}
			opts.ChatID = loadChatID(dynamicCtx, stateStore, cfg.Name)
			if opts.ChatID == 0 {
				// Fall back to the chat ID stored at channel creation time so the
				// bot can send messages before any inbound user message arrives.
				if tps, ok := cfg.PlatformState.(channel.TelegramPlatformState); ok && tps.ChatID != 0 {
					opts.ChatID = tps.ChatID
				}
			}
			opts.OnChatID = saveChatIDFunc(stateStore, cfg.Name)
			opts.MediaDir = filepath.Join(r.baseDir, string(userID), "memory", "media")
			tg := channel.NewDynamicTelegram(token, cfg.Name, cfg.Description, cfg.AllowedUsers, opts)
			channels[tg.Info().ID] = tg
		default:
			slog.Warn("skipping dynamic channel with unsupported type", "channel", cfg.Name, "type", cfg.Type)
		}
	}

	slog.Debug("built dynamic channels", "user", userID, "count", len(channels))
	return channels, channel.FanIn(dynamicCtx, channels)
}

// registerProviderTools loads existing connections and registers
// provider-specific MCP tools for connections that already have credentials stored.
// DEPRECATED: This function bridges old connections to the new credential system.
// It will be removed once all connections are migrated to credential sets.
func (r *Router) registerProviderTools(ctx context.Context, h *mcp.Handler, mgr *connection.Manager, credMgr *credential.Manager, googleDepsMap map[credential.CredentialSetID]googletools.Deps, monzoDepsMap map[credential.CredentialSetID]monzotools.Deps) {
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

		// Bridge: migrate the connection's tokens into a credential set so
		// the new tool handlers can find them.
		setID := credential.NewCredentialSetID(string(conn.ProviderID), conn.Label)
		migrateConnectionToCredentialSet(ctx, conn, creds, p, credMgr, setID)

		r.registerToolsForProvider(h, setID, credMgr, p, googleDepsMap, monzoDepsMap)
	}
}

// migrateConnectionToCredentialSet bridges an old connection into the new
// credential system by creating a credential set and copying the tokens.
func migrateConnectionToCredentialSet(ctx context.Context, conn connection.Connection, creds *connection.Credentials, p *provider.Provider, credMgr *credential.Manager, setID credential.CredentialSetID) {
	existing, err := credMgr.Get(ctx, setID)
	if err != nil {
		slog.Warn("failed to check existing credential set", "set", setID, "err", err)
		return
	}
	if existing == nil {
		if _, err := credMgr.Add(ctx, string(conn.ProviderID), conn.Label, conn.Channel); err != nil {
			slog.Warn("failed to create credential set from connection", "connection", conn.ID, "err", err)
			return
		}
	}

	// Copy OAuth client credentials from provider config.
	if p.OAuth2 != nil {
		if err := credMgr.SetField(ctx, setID, "client_id", p.OAuth2.ClientID); err != nil {
			slog.Warn("failed to set client_id from provider", "set", setID, "err", err)
		}
		if err := credMgr.SetField(ctx, setID, "client_secret", p.OAuth2.ClientSecret); err != nil {
			slog.Warn("failed to set client_secret from provider", "set", setID, "err", err)
		}
	}

	// Copy OAuth tokens.
	tokens := &credential.OAuthTokens{
		AccessToken:  creds.AccessToken,
		RefreshToken: creds.RefreshToken,
		ExpiresAt:    creds.ExpiresAt,
	}
	if err := credMgr.SetOAuthTokens(ctx, setID, tokens); err != nil {
		slog.Warn("failed to copy oauth tokens to credential set", "set", setID, "err", err)
	}
}

// registerToolsForProvider adds a credential set to the provider's tool set
// and re-registers tools with the updated deps map.
func (r *Router) registerToolsForProvider(h *mcp.Handler, setID credential.CredentialSetID, credMgr *credential.Manager, p *provider.Provider, googleDepsMap map[credential.CredentialSetID]googletools.Deps, monzoDepsMap map[credential.CredentialSetID]monzotools.Deps) {
	var oauthCfg *oauth.OAuth2Config
	if p.OAuth2 != nil {
		oauthCfg = &oauth.OAuth2Config{
			AuthURL:      p.OAuth2.AuthURL,
			TokenURL:     p.OAuth2.TokenURL,
			ClientID:     p.OAuth2.ClientID,
			ClientSecret: p.OAuth2.ClientSecret,
			Scopes:       p.OAuth2.Scopes,
			ExtraParams:  p.OAuth2.ExtraParams,
		}
	}
	deps := providerutil.Deps{
		CredSetID:   setID,
		Manager:     credMgr,
		OAuthConfig: oauthCfg,
	}

	switch p.ID {
	case provider.GoogleProviderID:
		googleDepsMap[setID] = deps
		googletools.RegisterTools(h, googleDepsMap)
		slog.Debug("registered google workspace tools", "credential_set", setID, "total_sets", len(googleDepsMap))
	case provider.MonzoProviderID:
		monzoDepsMap[setID] = deps
		monzotools.RegisterTools(h, monzoDepsMap)
		slog.Debug("registered monzo tools", "credential_set", setID, "total_sets", len(monzoDepsMap))
	default:
		slog.Warn("unsupported provider for tool registration", "provider", p.ID)
	}
}

// unregisterToolsForProvider removes a credential set from the provider's tool set.
// If no sets remain, the tools are removed entirely.
func (r *Router) unregisterToolsForProvider(h *mcp.Handler, setID credential.CredentialSetID, googleDepsMap map[credential.CredentialSetID]googletools.Deps, monzoDepsMap map[credential.CredentialSetID]monzotools.Deps) {
	if _, ok := googleDepsMap[setID]; ok {
		delete(googleDepsMap, setID)
		if len(googleDepsMap) == 0 {
			googletools.UnregisterTools(h)
			slog.Info("unregistered google workspace tools (no credential sets remain)")
		} else {
			googletools.RegisterTools(h, googleDepsMap)
			slog.Info("updated google workspace tools after disconnect", "removed", setID, "remaining", len(googleDepsMap))
		}
		return
	}

	if _, ok := monzoDepsMap[setID]; ok {
		delete(monzoDepsMap, setID)
		if len(monzoDepsMap) == 0 {
			monzotools.UnregisterTools(h)
			slog.Info("unregistered monzo tools (no credential sets remain)")
		} else {
			monzotools.RegisterTools(h, monzoDepsMap)
			slog.Info("updated monzo tools after disconnect", "removed", setID, "remaining", len(monzoDepsMap))
		}
		return
	}
}
