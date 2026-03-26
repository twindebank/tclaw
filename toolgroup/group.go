package toolgroup

import "tclaw/claudecli"

// ToolGroup is a named set of related tools that can be composed to build
// channel permissions. Channels pick multiple groups to build up their tools
// additively — you start with nothing and add what you need.
type ToolGroup string

const (
	GroupCoreTools         ToolGroup = "core_tools"
	GroupAllBuiltins       ToolGroup = "all_builtins"
	GroupSafeBuiltins      ToolGroup = "safe_builtins"
	GroupChannelMessaging  ToolGroup = "channel_messaging"
	GroupChannelManagement ToolGroup = "channel_management"
	GroupScheduling        ToolGroup = "scheduling"
	GroupDevWorkflow       ToolGroup = "dev_workflow"
	GroupRepoMonitoring    ToolGroup = "repo_monitoring"
	GroupGSuiteRead        ToolGroup = "gsuite_read"
	GroupGSuiteWrite       ToolGroup = "gsuite_write"
	GroupPersonalServices  ToolGroup = "personal_services"
	GroupConnections       ToolGroup = "connections"
	GroupTelegramClient    ToolGroup = "telegram_client"
	GroupOnboarding        ToolGroup = "onboarding"
	GroupSecretForm        ToolGroup = "secret_form"
)

// GroupInfo describes a tool group for display in the system prompt and tool descriptions.
type GroupInfo struct {
	Group       ToolGroup
	Description string
}

// AllGroups returns info about all available tool groups.
func AllGroups() []GroupInfo {
	return []GroupInfo{
		{GroupCoreTools, "Bash shell, file operations (read/write/edit/glob/grep), web access (fetch/search), and model management. The foundation most channels need."},
		{GroupAllBuiltins, "All built-in commands: stop, compact, login, auth, and all reset levels (session/memories/project/everything)."},
		{GroupSafeBuiltins, "Safe built-in commands only: stop, compact, session reset, memories reset. No project/everything reset or auth commands."},
		{GroupChannelMessaging, "Send messages to other channels (channel_send, channel_send_when_free), check if channels are busy (channel_is_busy), and tear down the current channel (channel_done). Cannot create new channels."},
		{GroupChannelManagement, "Full channel lifecycle: create, delete, edit, list channels, plus everything in channel_messaging. Required for orchestrating ephemeral channels."},
		{GroupScheduling, "Create, edit, delete, pause, and resume cron schedules."},
		{GroupDevWorkflow, "Dev workflow: start/end/cancel dev sessions, view status and logs, deploy to production. For channels that do code work."},
		{GroupRepoMonitoring, "Monitor external git repositories: add, sync, view logs, list, remove. Read-only — for tracking changes, not making them."},
		{GroupGSuiteRead, "Google Workspace read-only: list and read emails, list calendar events, read workspace data. Cannot send emails or create events."},
		{GroupGSuiteWrite, "Google Workspace full access: send emails, create/update calendar events, edit docs and sheets. Includes all read capabilities."},
		{GroupPersonalServices, "Personal service integrations: TfL transport, restaurant reservations, banking (Open Banking), Monzo."},
		{GroupConnections, "Manage OAuth connections to external services and remote MCP server connections."},
		{GroupTelegramClient, "Telegram Client API (MTProto): authenticate, configure bots via BotFather, manage chats, read message history."},
		{GroupOnboarding, "New user onboarding flow: track progress, deliver tips, manage setup phases."},
		{GroupSecretForm, "Collect sensitive information (API keys, tokens, passwords) via secure web forms. Values go directly to encrypted storage, never through chat."},
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
	claudecli.ToolAgent, claudecli.ToolTask, claudecli.ToolTaskStop,
	claudecli.ToolTodoWrite, claudecli.ToolToolSearch, claudecli.ToolSkill,
	claudecli.ToolAskUserQuestion, claudecli.ToolEnterPlanMode, claudecli.ToolExitPlanMode,
	claudecli.ToolEnterWorktree, claudecli.ToolListMcpResources, claudecli.ToolReadMcpResource,
}

// groupTools maps each tool group to its constituent tools.
var groupTools = map[ToolGroup][]claudecli.Tool{
	GroupCoreTools: {
		claudecli.ToolBash,
		claudecli.ToolRead, claudecli.ToolEdit, claudecli.ToolWrite,
		claudecli.ToolGlob, claudecli.ToolGrep,
		claudecli.ToolWebFetch, claudecli.ToolWebSearch,
		MCPToolModelAll,
	},

	GroupAllBuiltins: {
		claudecli.BuiltinStop,
		claudecli.BuiltinCompact,
		claudecli.BuiltinLogin,
		claudecli.BuiltinAuth,
		claudecli.BuiltinReset,
	},

	GroupSafeBuiltins: {
		claudecli.BuiltinStop,
		claudecli.BuiltinCompact,
		claudecli.BuiltinResetSession,
		claudecli.BuiltinResetMemories,
	},

	GroupChannelMessaging: {
		MCPToolChannelSend,
		MCPToolSendWhenFree,
		claudecli.Tool("mcp__tclaw__channel_is_busy"),
		MCPToolChannelDone,
	},

	GroupChannelManagement: {
		claudecli.Tool("mcp__tclaw__channel_create"),
		claudecli.Tool("mcp__tclaw__channel_delete"),
		claudecli.Tool("mcp__tclaw__channel_edit"),
		claudecli.Tool("mcp__tclaw__channel_list"),
		claudecli.Tool("mcp__tclaw__channel_notify"),
		MCPToolChannelDone,
		claudecli.Tool("mcp__tclaw__channel_is_busy"),
		MCPToolChannelSend,
		MCPToolSendWhenFree,
	},

	GroupScheduling: {
		MCPToolScheduleAll,
	},

	GroupDevWorkflow: {
		MCPToolDevAll,
		MCPToolDeploy,
	},

	GroupRepoMonitoring: {
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

	GroupPersonalServices: {
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
