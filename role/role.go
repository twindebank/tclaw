package role

import (
	"tclaw/claudecli"
	"tclaw/provider"
)

// Role is a named preset of tool permissions. Channels and users can specify a
// role instead of listing individual tools. Roles and allowed_tools are mutually
// exclusive — use one or the other.
type Role string

const (
	Superuser Role = "superuser"
	Developer Role = "developer"
	Assistant Role = "assistant"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case Superuser, Developer, Assistant:
		return true
	}
	return false
}

// ValidRoles returns all known role names.
func ValidRoles() []Role {
	return []Role{Superuser, Developer, Assistant}
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
	switch r {
	case Superuser:
		return resolveSuperuser(channelContext), nil
	case Developer:
		return resolveDeveloper(), nil
	case Assistant:
		return resolveAssistant(channelContext), nil
	default:
		return nil, nil
	}
}

// file tools shared across developer and assistant roles.
var fileTools = []claudecli.Tool{
	claudecli.ToolRead,
	claudecli.ToolEdit,
	claudecli.ToolWrite,
	claudecli.ToolGlob,
	claudecli.ToolGrep,
}

// web tools shared across roles.
var webTools = []claudecli.Tool{
	claudecli.ToolWebFetch,
	claudecli.ToolWebSearch,
}

// all builtins.
var allBuiltins = []claudecli.Tool{
	claudecli.BuiltinStop,
	claudecli.BuiltinCompact,
	claudecli.BuiltinLogin,
	claudecli.BuiltinAuth,
	claudecli.BuiltinReset,
}

// MCP tool patterns for tclaw tools.
const (
	MCPToolAll           claudecli.Tool = "mcp__tclaw__*"
	MCPToolDevAll        claudecli.Tool = "mcp__tclaw__dev_*"
	MCPToolDeploy        claudecli.Tool = "mcp__tclaw__deploy"
	MCPToolScheduleAll   claudecli.Tool = "mcp__tclaw__schedule_*"
	MCPToolConnectionAll claudecli.Tool = "mcp__tclaw__connection_*"
	MCPToolRemoteMCPAll  claudecli.Tool = "mcp__tclaw__remote_mcp_*"
	MCPToolGoogleAll     claudecli.Tool = "mcp__tclaw__google_*"
	MCPToolMonzoAll      claudecli.Tool = "mcp__tclaw__monzo_*"
	MCPToolTflAll        claudecli.Tool = "mcp__tclaw__tfl_*"
	MCPToolModelAll      claudecli.Tool = "mcp__tclaw__model_*"
	MCPToolOnboardingAll claudecli.Tool = "mcp__tclaw__onboarding_*"
	MCPToolRepoAll       claudecli.Tool = "mcp__tclaw__repo_*"
)

// basic builtins for assistant.
var basicBuiltins = []claudecli.Tool{
	claudecli.BuiltinStop,
	claudecli.BuiltinCompact,
	claudecli.BuiltinResetSession,
	claudecli.BuiltinResetMemories,
}

func resolveSuperuser(ctx ChannelContext) []claudecli.Tool {
	// Superuser gets everything — use a wildcard pattern that matches all
	// MCP tools, plus all base tools and all builtins.
	tools := []claudecli.Tool{
		// All base Claude Code tools.
		claudecli.ToolBash, claudecli.ToolRead, claudecli.ToolEdit, claudecli.ToolWrite,
		claudecli.ToolGlob, claudecli.ToolGrep, claudecli.ToolLS, claudecli.ToolLSP,
		claudecli.ToolWebFetch, claudecli.ToolWebSearch, claudecli.ToolNotebookEdit,
		claudecli.ToolAgent, claudecli.ToolTask, claudecli.ToolTaskOutput, claudecli.ToolTaskStop,
		claudecli.ToolTodoWrite, claudecli.ToolToolSearch, claudecli.ToolSkill,
		claudecli.ToolAskUserQuestion, claudecli.ToolEnterPlanMode, claudecli.ToolExitPlanMode,
		claudecli.ToolEnterWorktree, claudecli.ToolListMcpResources, claudecli.ToolReadMcpResource,
		// All tclaw MCP tools.
		MCPToolAll,
	}
	tools = append(tools, allBuiltins...)
	tools = append(tools, providerToolPatterns(ctx)...)
	tools = append(tools, remoteMCPToolPatterns(ctx)...)
	return tools
}

func resolveDeveloper() []claudecli.Tool {
	tools := []claudecli.Tool{
		claudecli.ToolBash,
		claudecli.ToolAgent,
		claudecli.ToolLS,
		claudecli.ToolLSP,
		claudecli.ToolNotebookEdit,
		claudecli.ToolTask,
		claudecli.ToolTaskOutput,
		claudecli.ToolTaskStop,
		claudecli.ToolTodoWrite,
		claudecli.ToolToolSearch,
		claudecli.ToolSkill,
		claudecli.ToolAskUserQuestion,
		claudecli.ToolEnterPlanMode,
		claudecli.ToolExitPlanMode,
		claudecli.ToolEnterWorktree,
		claudecli.ToolListMcpResources,
		claudecli.ToolReadMcpResource,
	}
	tools = append(tools, fileTools...)
	tools = append(tools, webTools...)
	tools = append(tools, allBuiltins...)
	// Dev workflow, schedule, repo monitoring, model, and onboarding tools.
	tools = append(tools,
		MCPToolDevAll,
		MCPToolDeploy,
		MCPToolScheduleAll,
		MCPToolRepoAll,
		MCPToolModelAll,
		MCPToolOnboardingAll,
	)
	return tools
}

func resolveAssistant(ctx ChannelContext) []claudecli.Tool {
	tools := make([]claudecli.Tool, 0, 32)
	tools = append(tools, fileTools...)
	tools = append(tools, webTools...)
	tools = append(tools, basicBuiltins...)
	// Connection/remote MCP management, scheduling, repo monitoring, model, TfL, and onboarding.
	tools = append(tools,
		MCPToolConnectionAll,
		MCPToolRemoteMCPAll,
		MCPToolScheduleAll,
		MCPToolRepoAll,
		MCPToolModelAll,
		MCPToolTflAll,
		MCPToolOnboardingAll,
	)
	// Provider tools for connections on this channel.
	tools = append(tools, providerToolPatterns(ctx)...)
	// Remote MCP tool patterns for remote MCPs on this channel.
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
