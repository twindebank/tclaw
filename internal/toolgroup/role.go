package toolgroup

import (
	"tclaw/internal/claudecli"
)

// ChannelContext provides information about what credential sets and remote MCPs
// exist on a specific channel. Used when resolving tool lists to include
// dynamic tool patterns.
type ChannelContext struct {
	// PackageNames lists tool package names that have credential sets available
	// on this channel. Used by CredentialToolPatterns to generate glob patterns.
	PackageNames []string

	RemoteMCPNames []string
}

// ResolveGroupsWithContext returns the combined tool list for multiple groups,
// deduplicating, and including dynamic credential/remote MCP patterns.
func ResolveGroupsWithContext(groups []ToolGroup, ctx ChannelContext) []claudecli.Tool {
	tools := ResolveGroups(groups)
	tools = append(tools, CredentialToolPatterns(ctx)...)
	tools = append(tools, RemoteMCPToolPatterns(ctx)...)
	return tools
}

// CredentialToolPatterns returns MCP tool glob patterns for each tool package
// that has credential sets available on this channel.
func CredentialToolPatterns(ctx ChannelContext) []claudecli.Tool {
	var tools []claudecli.Tool
	for _, name := range ctx.PackageNames {
		tools = append(tools, claudecli.Tool("mcp__tclaw__"+name+"_*"))
	}
	return tools
}

// RemoteMCPToolPatterns returns MCP tool glob patterns for each connected remote MCP.
func RemoteMCPToolPatterns(ctx ChannelContext) []claudecli.Tool {
	var tools []claudecli.Tool
	for _, name := range ctx.RemoteMCPNames {
		tools = append(tools, claudecli.Tool("mcp__"+name+"__*"))
	}
	return tools
}

// MCP tool patterns for tclaw tools.
const (
	MCPToolAll               claudecli.Tool = "mcp__tclaw__*"
	MCPToolDevAll            claudecli.Tool = "mcp__tclaw__dev_*"
	MCPToolScheduleAll       claudecli.Tool = "mcp__tclaw__schedule_*"
	MCPToolCredentialAll     claudecli.Tool = "mcp__tclaw__credential_*"
	MCPToolRemoteMCPAll      claudecli.Tool = "mcp__tclaw__remote_mcp_*"
	MCPToolGoogleAll         claudecli.Tool = "mcp__tclaw__google_*"
	MCPToolMonzoAll          claudecli.Tool = "mcp__tclaw__monzo_*"
	MCPToolTflAll            claudecli.Tool = "mcp__tclaw__tfl_*"
	MCPToolModelAll          claudecli.Tool = "mcp__tclaw__model_*"
	MCPToolOnboardingAll     claudecli.Tool = "mcp__tclaw__onboarding_*"
	MCPToolRepoAll           claudecli.Tool = "mcp__tclaw__repo_*"
	MCPToolRestaurantAll     claudecli.Tool = "mcp__tclaw__restaurant_*"
	MCPToolChannelSend       claudecli.Tool = "mcp__tclaw__channel_send"
	MCPToolSecretFormAll     claudecli.Tool = "mcp__tclaw__secret_form_*"
	MCPToolTelegramClientAll claudecli.Tool = "mcp__tclaw__telegram_client_*"
	MCPToolChannelDone       claudecli.Tool = "mcp__tclaw__channel_done"
	MCPToolNotificationAll   claudecli.Tool = "mcp__tclaw__notification_*"
)
