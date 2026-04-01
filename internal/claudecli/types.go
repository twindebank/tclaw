// Package claudecli defines typed enums and event structs for the claude CLI's stream-json output
// format. It models permission modes, tool names, content block types, and streaming events. Pure
// data types with no I/O — used by the agent package to parse and route CLI output.
package claudecli

import "strings"

// PermissionMode controls how the Claude CLI handles tool permissions.
type PermissionMode string

const (
	PermissionDefault     PermissionMode = "default"           // prompts for approval on everything
	PermissionAcceptEdits PermissionMode = "acceptEdits"       // auto-approves file edits, prompts for rest
	PermissionPlan        PermissionMode = "plan"              // read-only, can't execute anything
	PermissionDontAsk     PermissionMode = "dontAsk"           // auto-approves allowed tools, skips others
	PermissionBypass      PermissionMode = "bypassPermissions" // auto-approves everything, ignores allowed list
)

// Model identifies a Claude model for the CLI.
type Model string

const (
	// Claude 4.6 family (latest)
	ModelOpus46   Model = "claude-opus-4-6"
	ModelSonnet46 Model = "claude-sonnet-4-6"

	// Claude 4.5 family
	ModelHaiku45 Model = "claude-haiku-4-5-20251001"

	// Claude 4.0 family
	ModelOpus4   Model = "claude-opus-4-20250514"
	ModelSonnet4 Model = "claude-sonnet-4-20250514"

	// Claude 3.7
	ModelSonnet37 Model = "claude-3-7-sonnet-20250219"

	// Claude 3.5
	ModelSonnet35v2 Model = "claude-3-5-sonnet-20241022"
	ModelSonnet35   Model = "claude-3-5-sonnet-20240620"
	ModelHaiku35    Model = "claude-3-5-haiku-20241022"

	// Claude 3.0
	ModelOpus3  Model = "claude-3-opus-20240229"
	ModelHaiku3 Model = "claude-3-haiku-20240307"

	// ModelAuto means no --model flag is passed, letting the CLI choose.
	ModelAuto Model = ""
)

// ShortName returns a human-friendly short name for the model,
// e.g. "opus-4.6" for ModelOpus46. Returns the raw model string
// for unrecognized models, stripping the "claude-" prefix.
func (m Model) ShortName() string {
	if short, ok := modelShortNames[m]; ok {
		return short
	}
	return strings.TrimPrefix(string(m), "claude-")
}

// modelShortNames maps model identifiers to short display names.
var modelShortNames = map[Model]string{
	ModelOpus46:     "opus-4.6",
	ModelSonnet46:   "sonnet-4.6",
	ModelHaiku45:    "haiku-4.5",
	ModelOpus4:      "opus-4",
	ModelSonnet4:    "sonnet-4",
	ModelSonnet37:   "sonnet-3.7",
	ModelSonnet35v2: "sonnet-3.5v2",
	ModelSonnet35:   "sonnet-3.5",
	ModelHaiku35:    "haiku-3.5",
	ModelOpus3:      "opus-3",
	ModelHaiku3:     "haiku-3",
}

// ShortNameToModel maps short display names back to Model constants.
// Includes both short names and full model IDs for flexible lookups.
var ShortNameToModel = func() map[string]Model {
	m := make(map[string]Model, len(modelShortNames)*2+1)
	for model, short := range modelShortNames {
		m[short] = model
		m[string(model)] = model
	}
	m["auto"] = ModelAuto
	return m
}()

// Tool identifies a Claude Code built-in tool.
// Supports pattern syntax for scoped permissions, e.g. Tool("Bash(git *)").
type Tool string

const (
	ToolBash             Tool = "Bash"
	ToolRead             Tool = "Read"
	ToolEdit             Tool = "Edit"
	ToolWrite            Tool = "Write"
	ToolGlob             Tool = "Glob"
	ToolGrep             Tool = "Grep"
	ToolLS               Tool = "LS"
	ToolLSP              Tool = "LSP"
	ToolWebFetch         Tool = "WebFetch"
	ToolWebSearch        Tool = "WebSearch"
	ToolNotebookEdit     Tool = "NotebookEdit"
	ToolAgent            Tool = "Agent"
	ToolTask             Tool = "Task"
	ToolTaskStop         Tool = "TaskStop"
	ToolTodoWrite        Tool = "TodoWrite"
	ToolToolSearch       Tool = "ToolSearch"
	ToolSkill            Tool = "Skill"
	ToolAskUserQuestion  Tool = "AskUserQuestion"
	ToolEnterPlanMode    Tool = "EnterPlanMode"
	ToolExitPlanMode     Tool = "ExitPlanMode"
	ToolEnterWorktree    Tool = "EnterWorktree"
	ToolListMcpResources Tool = "ListMcpResources"
	ToolReadMcpResource  Tool = "ReadMcpResource"
)

// Builtin tool constants — evaluated by tclaw only, never passed to the CLI.
// The builtin__ prefix lets them coexist with Claude Code tools in allowed_tools lists.
const (
	BuiltinReset         Tool = "builtin__reset" // wildcard: all reset levels
	BuiltinResetSession  Tool = "builtin__reset_session"
	BuiltinResetMemories Tool = "builtin__reset_memories"
	BuiltinResetProject  Tool = "builtin__reset_project"
	BuiltinResetAll      Tool = "builtin__reset_all"
	BuiltinStop          Tool = "builtin__stop"
	BuiltinCompact       Tool = "builtin__compact"
	BuiltinLogin         Tool = "builtin__login"
	BuiltinAuth          Tool = "builtin__auth"
)

// IsBuiltinTool reports whether t has the builtin__ prefix.
func IsBuiltinTool(t Tool) bool {
	return strings.HasPrefix(string(t), "builtin__")
}

// Scoped returns a tool with a pattern restriction, e.g. Bash("git *").
func (t Tool) Scoped(pattern string) Tool {
	return Tool(string(t) + "(" + pattern + ")")
}

// validModels is the set of known model identifiers.
var validModels = map[Model]bool{
	ModelOpus46: true, ModelSonnet46: true,
	ModelHaiku45: true,
	ModelOpus4:   true, ModelSonnet4: true,
	ModelSonnet37:   true,
	ModelSonnet35v2: true, ModelSonnet35: true, ModelHaiku35: true,
	ModelOpus3: true, ModelHaiku3: true,
}

// ValidModel reports whether m is a known model identifier.
func ValidModel(m Model) bool { return validModels[m] }

// ValidModels returns the list of known model identifiers.
func ValidModels() []Model {
	out := make([]Model, 0, len(validModels))
	for m := range validModels {
		out = append(out, m)
	}
	return out
}

// validPermissionModes is the set of known permission modes.
var validPermissionModes = map[PermissionMode]bool{
	PermissionDefault:     true,
	PermissionAcceptEdits: true,
	PermissionPlan:        true,
	PermissionDontAsk:     true,
	PermissionBypass:      true,
}

// ValidPermissionMode reports whether p is a known permission mode.
func ValidPermissionMode(p PermissionMode) bool { return validPermissionModes[p] }

// ValidPermissionModes returns the list of known permission modes.
func ValidPermissionModes() []PermissionMode {
	out := make([]PermissionMode, 0, len(validPermissionModes))
	for p := range validPermissionModes {
		out = append(out, p)
	}
	return out
}

// validTools is the set of known base tool names (without scope patterns).
var validTools = map[Tool]bool{
	ToolBash: true, ToolRead: true, ToolEdit: true, ToolWrite: true,
	ToolGlob: true, ToolGrep: true, ToolLS: true, ToolLSP: true,
	ToolWebFetch: true, ToolWebSearch: true, ToolNotebookEdit: true,
	ToolAgent: true, ToolTask: true, ToolTaskStop: true,
	ToolTodoWrite: true, ToolToolSearch: true, ToolSkill: true,
	ToolAskUserQuestion: true, ToolEnterPlanMode: true, ToolExitPlanMode: true,
	ToolEnterWorktree: true, ToolListMcpResources: true, ToolReadMcpResource: true,
}

// ValidTool reports whether t is a known tool, including scoped patterns like Bash(git *)
// and MCP tool patterns like mcp__server__* or mcp__server__tool_name.
func ValidTool(t Tool) bool {
	if validTools[t] {
		return true
	}
	s := string(t)
	// MCP tool patterns: mcp__<server>__<tool_or_glob>
	if strings.HasPrefix(s, "mcp__") {
		return true
	}
	// Builtin tool patterns: builtin__<command>
	if strings.HasPrefix(s, "builtin__") {
		return true
	}
	// Scoped pattern: BaseTool(pattern)
	if idx := strings.Index(s, "("); idx > 0 && strings.HasSuffix(s, ")") {
		base := Tool(s[:idx])
		return validTools[base]
	}
	return false
}
