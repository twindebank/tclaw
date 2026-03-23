package role

import "tclaw/claudecli"

// ToolGroup is a named set of related tools that can be composed to build
// channel permissions. Channels pick multiple groups to build up their tools
// additively — you start with nothing and add what you need.
type ToolGroup string

const (
	GroupBase           ToolGroup = "base"
	GroupBuiltins       ToolGroup = "builtins"
	GroupBuiltinsBasic  ToolGroup = "builtins_basic"
	GroupChannelSend    ToolGroup = "channel_send"
	GroupChannelOps     ToolGroup = "channel_ops"
	GroupScheduling     ToolGroup = "scheduling"
	GroupDev            ToolGroup = "dev"
	GroupRepo           ToolGroup = "repo"
	GroupGSuiteRead     ToolGroup = "gsuite_read"
	GroupGSuiteWrite    ToolGroup = "gsuite_write"
	GroupServices       ToolGroup = "services"
	GroupConnections    ToolGroup = "connections"
	GroupTelegramClient ToolGroup = "telegram_client"
	GroupOnboarding     ToolGroup = "onboarding"
	GroupSecretForm     ToolGroup = "secret_form"
)

// GroupInfo describes a tool group for display in the system prompt and tool descriptions.
type GroupInfo struct {
	Group       ToolGroup
	Description string
}

// AllGroups returns info about all available tool groups.
func AllGroups() []GroupInfo {
	return []GroupInfo{
		{GroupBase, "Core tools — bash, file ops (read/write/edit/glob/grep), web (fetch/search), model management"},
		{GroupBuiltins, "All built-in commands — stop, compact, login, auth, reset (all levels)"},
		{GroupBuiltinsBasic, "Safe built-in commands — stop, compact, session reset, memories reset"},
		{GroupChannelSend, "Cross-channel messaging — send, send_when_free, is_busy, done (no creation)"},
		{GroupChannelOps, "Channel orchestration — create, delete, edit, list, done, is_busy"},
		{GroupScheduling, "Cron schedule management"},
		{GroupDev, "Dev workflow — dev_start, dev_end, dev_cancel, dev_status, dev_logs, deploy"},
		{GroupRepo, "Repository monitoring — repo_add, repo_sync, repo_log, repo_list, repo_remove"},
		{GroupGSuiteRead, "Google Workspace read-only — list/read email, list calendar events"},
		{GroupGSuiteWrite, "Google Workspace write — send email, create/update calendar events, docs, sheets"},
		{GroupServices, "Personal services — TfL, restaurants, banking, Monzo"},
		{GroupConnections, "OAuth connection and remote MCP management"},
		{GroupTelegramClient, "Telegram Client API — MTProto auth, bot management, chat management"},
		{GroupOnboarding, "New user onboarding flow"},
		{GroupSecretForm, "Secure web form for collecting sensitive information"},
	}
}

// ValidGroup reports whether g is a known tool group.
func ValidGroup(g ToolGroup) bool {
	_, ok := groupTools[g]
	return ok
}

// ValidGroups returns all known tool group names.
func ValidGroups() []ToolGroup {
	groups := make([]ToolGroup, 0, len(groupTools))
	for g := range groupTools {
		groups = append(groups, g)
	}
	return groups
}

// GroupTools returns the tool list for a single group.
func GroupTools(g ToolGroup) []claudecli.Tool {
	return groupTools[g]
}

// ResolveGroups returns the combined tool list for multiple groups, deduplicating.
func ResolveGroups(groups []ToolGroup) []claudecli.Tool {
	seen := make(map[claudecli.Tool]bool)
	var tools []claudecli.Tool
	for _, g := range groups {
		for _, t := range groupTools[g] {
			if !seen[t] {
				seen[t] = true
				tools = append(tools, t)
			}
		}
	}
	return tools
}

// cliToolsAll is the full set of Claude Code CLI tools for superuser.
var cliToolsAll = []claudecli.Tool{
	claudecli.ToolBash, claudecli.ToolRead, claudecli.ToolEdit, claudecli.ToolWrite,
	claudecli.ToolGlob, claudecli.ToolGrep, claudecli.ToolLS, claudecli.ToolLSP,
	claudecli.ToolWebFetch, claudecli.ToolWebSearch, claudecli.ToolNotebookEdit,
	claudecli.ToolAgent, claudecli.ToolTask, claudecli.ToolTaskOutput, claudecli.ToolTaskStop,
	claudecli.ToolTodoWrite, claudecli.ToolToolSearch, claudecli.ToolSkill,
	claudecli.ToolAskUserQuestion, claudecli.ToolEnterPlanMode, claudecli.ToolExitPlanMode,
	claudecli.ToolEnterWorktree, claudecli.ToolListMcpResources, claudecli.ToolReadMcpResource,
}

// groupTools maps each tool group to its constituent tools.
var groupTools = map[ToolGroup][]claudecli.Tool{
	GroupBase: {
		claudecli.ToolBash,
		claudecli.ToolRead, claudecli.ToolEdit, claudecli.ToolWrite,
		claudecli.ToolGlob, claudecli.ToolGrep,
		claudecli.ToolWebFetch, claudecli.ToolWebSearch,
		MCPToolModelAll,
	},

	GroupBuiltins: {
		claudecli.BuiltinStop,
		claudecli.BuiltinCompact,
		claudecli.BuiltinLogin,
		claudecli.BuiltinAuth,
		claudecli.BuiltinReset,
	},

	GroupBuiltinsBasic: {
		claudecli.BuiltinStop,
		claudecli.BuiltinCompact,
		claudecli.BuiltinResetSession,
		claudecli.BuiltinResetMemories,
	},

	GroupChannelSend: {
		MCPToolChannelSend,
		MCPToolSendWhenFree,
		claudecli.Tool("mcp__tclaw__channel_is_busy"),
		MCPToolChannelDone,
	},

	GroupChannelOps: {
		claudecli.Tool("mcp__tclaw__channel_create"),
		claudecli.Tool("mcp__tclaw__channel_delete"),
		claudecli.Tool("mcp__tclaw__channel_edit"),
		claudecli.Tool("mcp__tclaw__channel_list"),
		MCPToolChannelDone,
		claudecli.Tool("mcp__tclaw__channel_is_busy"),
		MCPToolChannelSend,
		MCPToolSendWhenFree,
	},

	GroupScheduling: {
		MCPToolScheduleAll,
	},

	GroupDev: {
		MCPToolDevAll,
		MCPToolDeploy,
	},

	GroupRepo: {
		MCPToolRepoAll,
	},

	GroupGSuiteRead: {
		claudecli.Tool("mcp__tclaw__google_gmail_list"),
		claudecli.Tool("mcp__tclaw__google_gmail_read"),
		claudecli.Tool("mcp__tclaw__google_workspace"),
		claudecli.Tool("mcp__tclaw__google_workspace_schema"),
	},

	GroupGSuiteWrite: {
		MCPToolGoogleAll,
	},

	GroupServices: {
		MCPToolTflAll,
		MCPToolRestaurantAll,
		claudecli.Tool("mcp__tclaw__banking_*"),
		MCPToolMonzoAll,
	},

	GroupConnections: {
		MCPToolConnectionAll,
		MCPToolRemoteMCPAll,
	},

	GroupTelegramClient: {
		MCPToolTelegramClientAll,
	},

	GroupOnboarding: {
		MCPToolOnboardingAll,
	},

	GroupSecretForm: {
		MCPToolSecretFormAll,
	},
}
