package router

import (
	"context"
	"log/slog"

	"tclaw/internal/channel"
	"tclaw/internal/config"
	"tclaw/internal/mcp"
	"tclaw/internal/remotemcpproxy"
	"tclaw/internal/remotemcpstore"
	"tclaw/internal/user"
)

// injectInitialMessages delivers the initial_message for any channels that have
// one set, then clears the field in config so it fires exactly once.
func injectInitialMessages(ctx context.Context, userID user.ID, configWriter *config.Writer, chMap map[channel.ChannelID]channel.Channel, output chan<- channel.TaggedMessage) {
	channels, err := configWriter.ReadChannels(userID)
	if err != nil {
		slog.Error("failed to read channels for initial message injection", "user", userID, "err", err)
		return
	}

	for _, cfg := range channels {
		if cfg.InitialMessage == "" {
			continue
		}

		var targetID channel.ChannelID
		for id, ch := range chMap {
			if ch.Info().Name == cfg.Name {
				targetID = id
				break
			}
		}
		if targetID == "" {
			slog.Warn("initial_message: channel not found in channel map, skipping", "channel", cfg.Name)
			continue
		}

		// Clear the initial message in config before delivery to prevent
		// duplicate fires if the agent restarts before processing.
		if clearErr := configWriter.UpdateChannel(userID, cfg.Name, func(c *config.Channel) {
			c.InitialMessage = ""
		}); clearErr != nil {
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

// buildRegistryEntries converts config channels to registry entries.
func buildRegistryEntries(configChannels []config.Channel) []channel.RegistryEntry {
	entries := make([]channel.RegistryEntry, 0, len(configChannels))
	for _, cc := range configChannels {
		entry := channel.RegistryEntry{
			Info: channel.Info{
				Type:            cc.Type,
				Name:            cc.Name,
				Description:     cc.Description,
				Purpose:         cc.Purpose,
				Model:           string(cc.Model),
				MaxTurns:        cc.MaxTurns,
				OutputStyle:     cc.OutputStyle,
				AllowedTools:    resolveConfigChannelTools(cc),
				DisallowedTools: cc.DisallowedTools,
				CreatableGroups: toolGroupsToStrings(cc.CreatableGroups),
				NotifyLifecycle: cc.NotifyLifecycle,
			},
			Links:  cc.Links,
			Parent: cc.Parent,
		}
		entries = append(entries, entry)
	}
	return entries
}

// remoteMCPConfigEntries maps stored remote MCPs to --mcp-config entries that
// point at the per-user auth proxy. No upstream credentials are read here: the
// proxy injects them server-side at request time, keeping secrets out of the
// sandbox-readable config. Each entry carries only the benign proxy-hop token.
func remoteMCPConfigEntries(mcps []remotemcpstore.RemoteMCP, proxy *remotemcpproxy.Server, proxyToken string) []mcp.RemoteMCPEntry {
	entries := make([]mcp.RemoteMCPEntry, 0, len(mcps))
	for _, m := range mcps {
		entries = append(entries, mcp.RemoteMCPEntry{
			Name:    m.Name,
			URL:     proxy.RemoteURL(m.Name),
			Headers: map[string]string{remotemcpproxy.ProxyTokenHeader: proxyToken},
		})
	}
	return entries
}
