package channeltools

import (
	"context"
	"encoding/json"
	"sort"

	"tclaw/claudecli"
	"tclaw/mcp"
)

func toolListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "tool_list",
		Description: "List all available tool names that can be used in allowed_tools and disallowed_tools when creating or editing channels. Includes Claude Code tools, tclaw MCP tools, and builtin commands.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

type toolListResponse struct {
	ClaudeCodeTools []string `json:"claude_code_tools"`
	MCPTools        []string `json:"mcp_tools"`
	BuiltinCommands []string `json:"builtin_commands"`
}

// RegisterToolListTool registers the tool_list tool. Call this after all other
// tools have been registered so the MCP tool list is complete.
func RegisterToolListTool(handler *mcp.Handler) {
	handler.Register(toolListDef(), toolListHandler(handler))
}

func toolListHandler(handler *mcp.Handler) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		// Claude Code built-in tools.
		codeTools := []string{
			string(claudecli.ToolBash),
			string(claudecli.ToolRead),
			string(claudecli.ToolEdit),
			string(claudecli.ToolWrite),
			string(claudecli.ToolGlob),
			string(claudecli.ToolGrep),
			string(claudecli.ToolLS),
			string(claudecli.ToolLSP),
			string(claudecli.ToolWebFetch),
			string(claudecli.ToolWebSearch),
			string(claudecli.ToolNotebookEdit),
			string(claudecli.ToolAgent),
			string(claudecli.ToolTask),
			string(claudecli.ToolTaskStop),
			string(claudecli.ToolTodoWrite),
			string(claudecli.ToolToolSearch),
			string(claudecli.ToolSkill),
			string(claudecli.ToolAskUserQuestion),
			string(claudecli.ToolEnterPlanMode),
			string(claudecli.ToolExitPlanMode),
			string(claudecli.ToolEnterWorktree),
			string(claudecli.ToolListMcpResources),
			string(claudecli.ToolReadMcpResource),
		}

		// Builtin commands (evaluated by tclaw, not passed to CLI).
		builtins := []string{
			string(claudecli.BuiltinStop),
			string(claudecli.BuiltinCompact),
			string(claudecli.BuiltinLogin),
			string(claudecli.BuiltinAuth),
			string(claudecli.BuiltinReset),
			string(claudecli.BuiltinResetSession),
			string(claudecli.BuiltinResetMemories),
			string(claudecli.BuiltinResetProject),
			string(claudecli.BuiltinResetAll),
		}

		// MCP tools from the handler (fully qualified as mcp__tclaw__<name>).
		var mcpTools []string
		for _, td := range handler.ListTools() {
			mcpTools = append(mcpTools, "mcp__tclaw__"+td.Name)
		}
		sort.Strings(mcpTools)

		return json.Marshal(toolListResponse{
			ClaudeCodeTools: codeTools,
			MCPTools:        mcpTools,
			BuiltinCommands: builtins,
		})
	}
}
