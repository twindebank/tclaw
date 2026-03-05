package agent

import "encoding/json"

// PermissionMode controls how the Claude CLI handles tool permissions.
type PermissionMode string

const (
	PermissionDefault     PermissionMode = "default"
	PermissionAcceptEdits PermissionMode = "acceptEdits"
	PermissionPlan        PermissionMode = "plan"
	PermissionBypass      PermissionMode = "bypassPermissions"
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
)

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
	ToolTaskOutput       Tool = "TaskOutput"
	ToolTaskStop         Tool = "TaskStop"
	ToolTodoWrite        Tool = "TodoWrite"
	ToolToolSearch       Tool = "ToolSearch"
	ToolSkill            Tool = "Skill"
	ToolAskUserQuestion  Tool = "AskUserQuestion"
	ToolEnterPlanMode    Tool = "EnterPlanMode"
	ToolExitPlanMode     Tool = "ExitPlanMode"
	ToolEnterWorktree    Tool = "EnterWorktree"
	ToolListMcpResources Tool = "ListMcpResourcesTool"
	ToolReadMcpResource  Tool = "ReadMcpResourceTool"
)

// Scoped returns a tool with a pattern restriction, e.g. Bash("git *").
func (t Tool) Scoped(pattern string) Tool {
	return Tool(string(t) + "(" + pattern + ")")
}

// EventType identifies the kind of streaming event from the CLI.
type EventType string

const (
	EventSystem            EventType = "system"
	EventAssistant         EventType = "assistant"
	EventUser              EventType = "user"
	EventContentBlockStart EventType = "content_block_start"
	EventContentBlockDelta EventType = "content_block_delta"
	EventContentBlockStop  EventType = "content_block_stop"
	EventRateLimit         EventType = "rate_limit_event"
	EventResult            EventType = "result"
)

// ContentBlockType identifies the kind of content within a message.
type ContentBlockType string

const (
	ContentText     ContentBlockType = "text"
	ContentToolUse  ContentBlockType = "tool_use"
	ContentThinking ContentBlockType = "thinking"
)

// SystemEventSubtype identifies the kind of system event.
type SystemEventSubtype string

const (
	SystemSubtypeInit SystemEventSubtype = "init"
)

// Event is a single parsed line from the CLI's stream-json output.
type Event struct {
	Type EventType `json:"type"`
}

// SystemEvent is emitted at the start of a session with metadata like session_id.
type SystemEvent struct {
	Type      EventType          `json:"type"`
	Subtype   SystemEventSubtype `json:"subtype"`
	SessionID string             `json:"session_id"`
}

// AssistantEvent is the complete assistant message returned by --print mode.
type AssistantEvent struct {
	Type    EventType        `json:"type"`
	Message AssistantMessage `json:"message"`
}

type AssistantMessage struct {
	Content []ContentBlock `json:"content"`
}

type ContentBlock struct {
	Type ContentBlockType `json:"type"`
	Text string           `json:"text,omitempty"`

	// tool_use fields
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// thinking fields
	Thinking string `json:"thinking,omitempty"`
}

// UserEvent is emitted when Claude Code feeds a tool result back to the model.
type UserEvent struct {
	Type          EventType      `json:"type"`
	ToolUseResult *ToolUseResult `json:"tool_use_result,omitempty"`
}

type ToolUseResult struct {
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
}

// ContentBlockStartEvent marks the beginning of a new content block.
type ContentBlockStartEvent struct {
	Type         EventType    `json:"type"`
	ContentBlock ContentBlock `json:"content_block"`
}

// ContentDeltaEvent is a streaming text chunk.
type ContentDeltaEvent struct {
	Type  EventType `json:"type"`
	Delta Delta     `json:"delta"`
}

// DeltaType identifies the kind of streaming delta.
type DeltaType string

const (
	DeltaText     DeltaType = "text_delta"
	DeltaThinking DeltaType = "thinking_delta"
	DeltaInputJSON DeltaType = "input_json_delta"
)

type Delta struct {
	Type     DeltaType `json:"type"`
	Text     string    `json:"text,omitempty"`
	Thinking string    `json:"thinking,omitempty"`
}

// ResultEvent is the final summary emitted when the CLI finishes.
type ResultEvent struct {
	Type       EventType `json:"type"`
	IsError    bool      `json:"is_error"`
	Result     string    `json:"result"`
	DurationMs float64   `json:"duration_ms"`
	NumTurns   int       `json:"num_turns"`
	SessionID  string    `json:"session_id"`
	CostUSD    float64   `json:"total_cost_usd"`
}
