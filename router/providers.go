package router

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"path/filepath"

	"tclaw/channel"
	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/remotemcpstore"
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
// channel instances for each.
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

// buildRemoteMCPEntries loads remote MCPs and their auth tokens for MCP config generation.
func buildRemoteMCPEntries(ctx context.Context, mgr *remotemcpstore.Manager) []remoteMCPEntry {
	mcps, err := mgr.ListRemoteMCPs(ctx)
	if err != nil {
		slog.Error("failed to list remote mcps for config", "err", err)
		return nil
	}
	var entries []remoteMCPEntry
	for _, m := range mcps {
		entry := remoteMCPEntry{Name: m.Name, URL: m.URL}
		auth, authErr := mgr.GetRemoteMCPAuth(ctx, m.Name)
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

// remoteMCPEntry is the data needed to generate an MCP config entry.
type remoteMCPEntry struct {
	Name        string
	URL         string
	BearerToken string
}
