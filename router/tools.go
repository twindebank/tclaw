package router

import (
	"context"
	"log/slog"

	"tclaw/agent"
	"tclaw/channel"
	"tclaw/claudecli"
	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/role"
	"tclaw/user"
)

// channelToolSource holds the role or explicit tool lists for a channel.
// Exactly one of Role or AllowedTools should be set (they're mutually exclusive).
type channelToolSource struct {
	Role            role.Role
	AllowedTools    []string
	DisallowedTools []string
}

// buildChannelToolOverrides constructs the per-channel tool permission map.
// For each channel it resolves the effective tools from either a role or
// explicit allowed_tools lists. The resolution order is:
//  1. Channel-level role or allowed_tools (from config or dynamic store)
//  2. User-level role or allowed_tools (fallback)
//
// When a role is used, it's resolved via role.Resolve() with a ChannelContext
// that includes which providers and remote MCPs are available on that channel.
func buildChannelToolOverrides(
	allChMap map[channel.ChannelID]channel.Channel,
	registry *channel.Registry,
	ctx context.Context,
	userCfg user.Config,
	connMgr *connection.Manager,
) map[channel.ChannelID]agent.ChannelToolPermissions {
	overrides := make(map[channel.ChannelID]agent.ChannelToolPermissions)

	for chID, ch := range allChMap {
		name := ch.Info().Name

		// Determine the tool source for this channel via the registry.
		var src channelToolSource
		entry, err := registry.ByName(ctx, name)
		if err != nil {
			slog.Error("failed to look up channel in registry", "channel", name, "err", err)
		}
		if entry != nil {
			src = channelToolSource{
				Role:            entry.Role,
				AllowedTools:    entry.AllowedTools,
				DisallowedTools: entry.DisallowedTools,
			}
		}

		// Fall back to user-level if channel has neither role nor allowed_tools.
		if src.Role == "" && len(src.AllowedTools) == 0 {
			src.Role = userCfg.Role
			if src.Role == "" {
				// User-level explicit tools — convert to strings.
				src.AllowedTools = toolsToStrings(userCfg.AllowedTools)
				src.DisallowedTools = toolsToStrings(userCfg.DisallowedTools)
			}
		}

		// Resolve the final tool lists.
		var allowed, disallowed []claudecli.Tool

		if src.Role != "" {
			channelCtx := buildChannelContext(ctx, connMgr, name)
			allowed, disallowed = role.Resolve(src.Role, channelCtx)
			// Append channel-level disallowed_tools for surgical removal
			// alongside a role.
			disallowed = append(disallowed, toTools(src.DisallowedTools)...)
		} else {
			allowed = toTools(src.AllowedTools)
			disallowed = toTools(src.DisallowedTools)
		}

		if len(allowed) == 0 && len(disallowed) == 0 {
			continue
		}
		overrides[chID] = agent.ChannelToolPermissions{
			AllowedTools:    allowed,
			DisallowedTools: disallowed,
		}
	}

	return overrides
}

// buildChannelContext constructs the ChannelContext for role resolution by
// looking up which provider connections and remote MCPs are scoped to this
// channel.
func buildChannelContext(ctx context.Context, connMgr *connection.Manager, channelName string) role.ChannelContext {
	var channelCtx role.ChannelContext

	conns, err := connMgr.ListByChannel(ctx, channelName)
	if err != nil {
		slog.Error("failed to list connections for channel context", "channel", channelName, "err", err)
	} else {
		seen := make(map[string]bool)
		for _, c := range conns {
			pid := string(c.ProviderID)
			if !seen[pid] {
				channelCtx.ProviderIDs = append(channelCtx.ProviderIDs, pid)
				seen[pid] = true
			}
		}
	}

	mcps, err := connMgr.ListRemoteMCPsByChannel(ctx, channelName)
	if err != nil {
		slog.Error("failed to list remote mcps for channel context", "channel", channelName, "err", err)
	} else {
		for _, m := range mcps {
			channelCtx.RemoteMCPNames = append(channelCtx.RemoteMCPNames, m.Name)
		}
	}

	return channelCtx
}

// buildMCPConfigPaths generates per-channel MCP config files for channels that
// have channel-scoped remote MCPs. Returns a map of channel ID to config path.
func buildMCPConfigPaths(
	ctx context.Context,
	allChMap map[channel.ChannelID]channel.Channel,
	connMgr *connection.Manager,
	mcpConfigDir string,
	mcpAddr string,
	mcpToken string,
) map[channel.ChannelID]string {
	paths := make(map[channel.ChannelID]string)

	for chID, ch := range allChMap {
		name := ch.Info().Name

		mcps, err := connMgr.ListRemoteMCPsByChannel(ctx, name)
		if err != nil {
			slog.Error("failed to list remote mcps for channel config", "channel", name, "err", err)
			continue
		}

		// Only generate a per-channel config if there are channel-specific
		// remote MCPs. If all remote MCPs are global, the default config works.
		hasChannelScoped := false
		for _, m := range mcps {
			if m.Channel != "" {
				hasChannelScoped = true
				break
			}
		}
		if !hasChannelScoped {
			continue
		}

		var entries []mcp.RemoteMCPEntry
		for _, m := range mcps {
			entry := mcp.RemoteMCPEntry{Name: m.Name, URL: m.URL}
			auth, authErr := connMgr.GetRemoteMCPAuth(ctx, m.Name)
			if authErr != nil {
				slog.Warn("failed to load remote mcp auth for channel config", "name", m.Name, "err", authErr)
			}
			if auth != nil && auth.AccessToken != "" {
				entry.BearerToken = auth.AccessToken
			}
			entries = append(entries, entry)
		}

		path, err := mcp.GenerateChannelConfigFile(mcpConfigDir, mcpAddr, mcpToken, name, entries)
		if err != nil {
			slog.Error("failed to generate channel mcp config", "channel", name, "err", err)
			continue
		}
		paths[chID] = path
	}

	return paths
}

// toolsToStrings converts a claudecli.Tool slice to strings.
func toolsToStrings(tools []claudecli.Tool) []string {
	if len(tools) == 0 {
		return nil
	}
	ss := make([]string, len(tools))
	for i, t := range tools {
		ss[i] = string(t)
	}
	return ss
}

// toTools converts a string slice to a claudecli.Tool slice.
func toTools(ss []string) []claudecli.Tool {
	if len(ss) == 0 {
		return nil
	}
	tools := make([]claudecli.Tool, len(ss))
	for i, s := range ss {
		tools[i] = claudecli.Tool(s)
	}
	return tools
}
