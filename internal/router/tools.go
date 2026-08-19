package router

import (
	"context"
	"log/slog"

	"tclaw/internal/agent"
	"tclaw/internal/channel"
	"tclaw/internal/claudecli"
	"tclaw/internal/config"
	"tclaw/internal/credential"
	"tclaw/internal/mcp"
	"tclaw/internal/remotemcpproxy"
	"tclaw/internal/remotemcpstore"
	"tclaw/internal/toolgroup"
	"tclaw/internal/user"
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

// buildChannelModels maps each channel ID to its configured per-channel model
// override. Channels without a model are omitted so the agent falls back to the
// runtime override or user-level model.
func buildChannelModels(
	allChMap map[channel.ChannelID]channel.Channel,
	registry *channel.Registry,
) map[channel.ChannelID]claudecli.Model {
	models := make(map[channel.ChannelID]claudecli.Model)
	for chID, ch := range allChMap {
		entry := registry.ByName(ch.Info().Name)
		if entry == nil || entry.Model == "" {
			continue
		}
		models[chID] = claudecli.Model(entry.Model)
	}
	return models
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

	all, err := connMgr.ListRemoteMCPs(ctx)
	if err != nil {
		slog.Error("failed to list remote mcps for channel context", "channel", channelName, "err", err)
	} else {
		// Must match what this channel's MCP config carries, or the agent is
		// handed a server whose tools it is then denied.
		global, scoped := partitionRemoteMCPs(all)
		channelCtx.RemoteMCPNames = append(remoteMCPNames(global), remoteMCPNames(scoped[channelName])...)
	}

	return channelCtx
}

// buildMCPConfigPaths generates per-channel MCP config files for channels that
// have channel-scoped remote MCPs. Returns a map of channel ID to config path.
//
// A channel's file carries the global servers plus its own; a channel with no
// servers of its own gets no file and falls back to the default config, which
// carries the global ones.
func buildMCPConfigPaths(
	ctx context.Context,
	allChMap map[channel.ChannelID]channel.Channel,
	connMgr *remotemcpstore.Manager,
	proxy *remotemcpproxy.Server,
	proxyToken string,
	mcpConfigDir string,
	mcpAddr string,
	mcpToken string,
) map[channel.ChannelID]string {
	paths := make(map[channel.ChannelID]string)

	all, err := connMgr.ListRemoteMCPs(ctx)
	if err != nil {
		slog.Error("failed to list remote mcps for channel configs", "err", err)
		return paths
	}

	global, scoped := partitionRemoteMCPs(all)

	for chID, ch := range allChMap {
		name := ch.Info().Name

		own := scoped[name]
		if len(own) == 0 {
			continue
		}

		mcps := make([]remotemcpstore.RemoteMCP, 0, len(global)+len(own))
		mcps = append(mcps, global...)
		mcps = append(mcps, own...)

		entries := remoteMCPConfigEntries(mcps, proxy, proxyToken)

		path, err := mcp.GenerateChannelConfigFile(mcpConfigDir, mcpAddr, mcpToken, name, entries)
		if err != nil {
			slog.Error("failed to generate channel mcp config", "channel", name, "err", err)
			continue
		}
		slog.Info("channel mcp config generated", "channel", name, "servers", remoteMCPNames(mcps))
		paths[chID] = path
	}

	return paths
}

// partitionRemoteMCPs splits remote MCPs into the global ones (no channel set)
// and the channel-scoped ones, keyed by channel name.
func partitionRemoteMCPs(all []remotemcpstore.RemoteMCP) (global []remotemcpstore.RemoteMCP, scoped map[string][]remotemcpstore.RemoteMCP) {
	scoped = make(map[string][]remotemcpstore.RemoteMCP)
	for _, m := range all {
		if m.Channel == "" {
			global = append(global, m)
			continue
		}
		scoped[m.Channel] = append(scoped[m.Channel], m)
	}
	return global, scoped
}

// remoteMCPNames lists server names for logging which servers a channel resolved to.
func remoteMCPNames(mcps []remotemcpstore.RemoteMCP) []string {
	names := make([]string, 0, len(mcps))
	for _, m := range mcps {
		names = append(names, m.Name)
	}
	return names
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
