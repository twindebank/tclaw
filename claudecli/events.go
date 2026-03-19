package claudecli

import "encoding/json"

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
// When the CLI cannot authenticate, the Error field is set (e.g. "authentication_failed")
// and the message content is a human-readable error like "Not logged in · Please run /login".
type AssistantEvent struct {
	Type    EventType        `json:"type"`
	Message AssistantMessage `json:"message"`
	Error   string           `json:"error,omitempty"`
}

// AssistantErrorAuthFailed is the error string the CLI returns when not logged in.
const AssistantErrorAuthFailed = "authentication_failed"

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
	Type          EventType       `json:"type"`
	ToolUseResult json.RawMessage `json:"tool_use_result,omitempty"`
}

// ToolResultMeta captures common fields from the tool_use_result payload.
// Different tools include different fields; we extract what we can.
type ToolResultMeta struct {
	DurationSeconds float64 `json:"durationSeconds,omitempty"`
	Query           string  `json:"query,omitempty"`

	// Bash/Read/Edit style results
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
	DeltaText      DeltaType = "text_delta"
	DeltaThinking  DeltaType = "thinking_delta"
	DeltaInputJSON DeltaType = "input_json_delta"
)

type Delta struct {
	Type     DeltaType `json:"type"`
	Text     string    `json:"text,omitempty"`
	Thinking string    `json:"thinking,omitempty"`
}

// RateLimitEvent is emitted when the CLI encounters a rate limit and is waiting to retry.
// The CLI typically retries automatically; this event is informational for the UI.
type RateLimitEvent struct {
	Type         EventType `json:"type"`
	RetryAfterMs int       `json:"retryAfterMs,omitempty"`
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

	// ModelUsage is a per-model breakdown of token usage and cost.
	// Keys are model identifiers (may include context window suffix, e.g. "claude-opus-4-6[1m]").
	ModelUsage map[string]ModelUsage `json:"modelUsage,omitempty"`
}

// ModelUsage holds token counts and cost for a single model within a turn.
type ModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	CostUSD                  float64 `json:"costUSD"`
}
