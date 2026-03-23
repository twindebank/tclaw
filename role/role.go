package role

import (
	"tclaw/claudecli"
	"tclaw/provider"
)

// Role is a named preset of tool permissions. Each role is a predefined
// combination of tool groups. Channels and users can specify a role, a list
// of tool groups, or explicit tool lists — these are mutually exclusive.
type Role string

const (
	Superuser Role = "superuser"
	Developer Role = "developer"
	Assistant Role = "assistant"
	Monitor   Role = "monitor"
)

// RoleInfo describes a role for display in the system prompt and tool descriptions.
type RoleInfo struct {
	Role        Role
	Description string
	Groups      []ToolGroup
}

// AllRoleInfo returns info about all available roles.
func AllRoleInfo() []RoleInfo {
	return []RoleInfo{
		{Superuser, "Full system access — all tools, all builtins", nil},
		{Assistant, "Personal assistant — web, services, scheduling, cross-channel messaging", roleGroups[Assistant]},
		{Developer, "Code and dev workflow — bash, dev tools, deployment, repo monitoring", roleGroups[Developer]},
		{Monitor, "Watch, report, and orchestrate — observation, channel creation, schedule management", roleGroups[Monitor]},
	}
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case Superuser, Developer, Assistant, Monitor:
		return true
	}
	return false
}

// ValidRoles returns all known role names.
func ValidRoles() []Role {
	return []Role{Superuser, Developer, Assistant, Monitor}
}

// DefaultCreatableRoles returns the default set of roles a channel with the
// given role is allowed to create. Returns nil (empty) for roles that should
// not create channels by default.
func DefaultCreatableRoles(r Role) []Role {
	switch r {
	case Superuser:
		return ValidRoles()
	case Monitor:
		return []Role{Developer, Assistant}
	default:
		return nil
	}
}

// DefaultCreatableGroups returns the default set of tool groups a channel with
// the given role is allowed to delegate when creating channels. Returns all
// groups for superuser, a safe subset for monitor, and nil for others.
func DefaultCreatableGroups(r Role) []ToolGroup {
	switch r {
	case Superuser:
		return ValidGroups()
	case Monitor:
		// Monitor can delegate groups that developer and assistant channels need,
		// but NOT channel_ops (prevents chain spawning).
		return []ToolGroup{
			GroupBase, GroupBuiltinsBasic, GroupChannelSend,
			GroupDev, GroupRepo, GroupScheduling,
			GroupGSuiteRead, GroupGSuiteWrite,
			GroupServices, GroupConnections,
			GroupTelegramClient, GroupOnboarding, GroupSecretForm,
		}
	default:
		return nil
	}
}

// RoleGroups returns the tool groups for a role. Returns nil for superuser
// (which uses a different resolution path) and unknown roles.
func RoleGroups(r Role) []ToolGroup {
	return roleGroups[r]
}

// roleGroups maps each role to its constituent tool groups.
var roleGroups = map[Role][]ToolGroup{
	Assistant: {
		GroupBase, GroupBuiltinsBasic, GroupChannelSend,
		GroupScheduling, GroupGSuiteRead, GroupGSuiteWrite,
		GroupServices, GroupConnections, GroupTelegramClient,
		GroupOnboarding, GroupSecretForm,
	},
	Developer: {
		GroupBase, GroupBuiltins, GroupChannelSend,
		GroupDev, GroupRepo,
	},
	Monitor: {
		GroupBase, GroupBuiltinsBasic, GroupChannelOps,
		GroupChannelSend, GroupScheduling,
	},
}

// ChannelContext provides information about what connections and remote MCPs
// exist on a specific channel. The role resolver uses this to include the
// correct provider tool patterns (e.g. google_* only if a Google connection
// exists on this channel).
type ChannelContext struct {
	// ProviderIDs lists the provider IDs for connections on this channel
	// (e.g. ["google"]). Used to include provider-specific tools.
	ProviderIDs []string

	// RemoteMCPNames lists the names of remote MCP servers connected on
	// this channel (e.g. ["linear", "notion"]). Used to include remote
	// MCP tool patterns.
	RemoteMCPNames []string
}

// Resolve returns the allowed and disallowed tool lists for the given role,
// taking into account which connections and remote MCPs exist on the channel.
func Resolve(r Role, channelContext ChannelContext) (allowed []claudecli.Tool, disallowed []claudecli.Tool) {
	if r == Superuser {
		return resolveSuperuser(channelContext), nil
	}

	groups, ok := roleGroups[r]
	if !ok {
		return nil, nil
	}

	tools := ResolveGroups(groups)
	tools = append(tools, providerToolPatterns(channelContext)...)
	tools = append(tools, remoteMCPToolPatterns(channelContext)...)
	return tools, nil
}

// ResolveToolGroups returns the tool list for a set of tool groups, including
// dynamic provider and remote MCP patterns from the channel context.
func ResolveToolGroups(groups []ToolGroup, channelContext ChannelContext) []claudecli.Tool {
	tools := ResolveGroups(groups)
	tools = append(tools, providerToolPatterns(channelContext)...)
	tools = append(tools, remoteMCPToolPatterns(channelContext)...)
	return tools
}

// MCP tool patterns for tclaw tools.
const (
	MCPToolAll               claudecli.Tool = "mcp__tclaw__*"
	MCPToolDevAll            claudecli.Tool = "mcp__tclaw__dev_*"
	MCPToolDeploy            claudecli.Tool = "mcp__tclaw__deploy"
	MCPToolScheduleAll       claudecli.Tool = "mcp__tclaw__schedule_*"
	MCPToolConnectionAll     claudecli.Tool = "mcp__tclaw__connection_*"
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
	MCPToolSendWhenFree      claudecli.Tool = "mcp__tclaw__channel_send_when_free"
)

func resolveSuperuser(ctx ChannelContext) []claudecli.Tool {
	// Superuser gets everything — all CLI tools + MCP wildcard + all builtins.
	tools := make([]claudecli.Tool, len(cliToolsAll))
	copy(tools, cliToolsAll)
	tools = append(tools, MCPToolAll)
	tools = append(tools, GroupTools(GroupBuiltins)...)
	tools = append(tools, providerToolPatterns(ctx)...)
	tools = append(tools, remoteMCPToolPatterns(ctx)...)
	return tools
}

// providerToolPatterns returns MCP tool glob patterns for each connected provider.
func providerToolPatterns(ctx ChannelContext) []claudecli.Tool {
	var tools []claudecli.Tool
	for _, pid := range ctx.ProviderIDs {
		switch provider.ProviderID(pid) {
		case provider.GoogleProviderID:
			tools = append(tools, MCPToolGoogleAll)
		case provider.MonzoProviderID:
			tools = append(tools, MCPToolMonzoAll)
		}
	}
	return tools
}

// remoteMCPToolPatterns returns MCP tool glob patterns for each connected remote MCP.
func remoteMCPToolPatterns(ctx ChannelContext) []claudecli.Tool {
	var tools []claudecli.Tool
	for _, name := range ctx.RemoteMCPNames {
		tools = append(tools, claudecli.Tool("mcp__"+name+"__*"))
	}
	return tools
}
