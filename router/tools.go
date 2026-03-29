package router

import (
	"context"
	"log/slog"

	"tclaw/agent"
	"tclaw/channel"
	"tclaw/claudecli"
	"tclaw/config"
	"tclaw/credential"
	"tclaw/mcp"
	"tclaw/remotemcpstore"
	"tclaw/toolgroup"
	"tclaw/user"
)

// channelToolSource holds the resolved tool lists for a channel.
type channelToolSource struct {
	AllowedTools    []string
	DisallowedTools []string
}

// buildChannelToolOverrides constructs the per-channel tool permission map.
// For each channel it resolves the effective tools from either a role or
// explicit allowed_tools lists. The resolution order is:
//  1. Channel-level role or allowed_tools (from config or dynamic store)
//  2. User-level role or allowed_tools (fallback)
//
// Provider and remote MCP tool patterns are added dynamically based on which
// connections and remote MCPs are available on that channel.
func buildChannelToolOverrides(
	allChMap map[channel.ChannelID]channel.Channel,
	registry *channel.Registry,
	ctx context.Context,
	userCfg user.Config,
	connMgr *remotemcpstore.Manager,
	credMgr *credential.Manager,
) map[channel.ChannelID]agent.ChannelToolPermissions {
	overrides := make(map[channel.ChannelID]agent.ChannelToolPermissions)

	for chID, ch := range allChMap {
		name := ch.Info().Name

		// Determine the tool source for this channel via the registry.
		var src channelToolSource
		entry := registry.ByName(name)
		if entry != nil {
			src = channelToolSource{
				AllowedTools:    entry.AllowedTools,
				DisallowedTools: entry.DisallowedTools,
			}
		}

		// Fall back to user-level if channel has no permissions set.
		if len(src.AllowedTools) == 0 {
			src.AllowedTools = toolsToStrings(userCfg.AllowedTools)
			src.DisallowedTools = toolsToStrings(userCfg.DisallowedTools)
		}

		// Add dynamic credential and remote MCP tool patterns.
		channelCtx := buildChannelContext(ctx, connMgr, credMgr, name)
		var extraTools []claudecli.Tool
		extraTools = append(extraTools, toolgroup.CredentialToolPatterns(channelCtx)...)
		extraTools = append(extraTools, toolgroup.RemoteMCPToolPatterns(channelCtx)...)

		allowed := toTools(src.AllowedTools)
		allowed = append(allowed, extraTools...)
		disallowed := toTools(src.DisallowedTools)

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
func buildChannelContext(ctx context.Context, connMgr *remotemcpstore.Manager, credMgr *credential.Manager, channelName string) toolgroup.ChannelContext {
	var channelCtx toolgroup.ChannelContext

	// Credential-based tool packages.
	credSets, err := credMgr.ListByChannel(ctx, channelName)
	if err != nil {
		slog.Error("failed to list credential sets for channel context", "channel", channelName, "err", err)
	} else {
		seen := make(map[string]bool)
		for _, s := range credSets {
			if !seen[s.Package] {
				channelCtx.PackageNames = append(channelCtx.PackageNames, s.Package)
				seen[s.Package] = true
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
	connMgr *remotemcpstore.Manager,
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

// resolveConfigChannelTools resolves a config.Channel's tool permissions
// to a flat []string list. ToolGroups are resolved via toolgroup.ResolveGroups;
// explicit AllowedTools are passed through as-is.
func resolveConfigChannelTools(cc config.Channel) []string {
	if len(cc.ToolGroups) > 0 {
		tools := toolgroup.ResolveGroups(cc.ToolGroups)
		ss := make([]string, len(tools))
		for i, t := range tools {
			ss[i] = string(t)
		}
		return ss
	}
	return cc.AllowedTools
}

// toolGroupsToStrings converts a []toolgroup.ToolGroup to []string.
func toolGroupsToStrings(groups []toolgroup.ToolGroup) []string {
	if len(groups) == 0 {
		return nil
	}
	ss := make([]string, len(groups))
	for i, g := range groups {
		ss[i] = string(g)
	}
	return ss
}
