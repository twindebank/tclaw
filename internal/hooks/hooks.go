// Package hooks implements the Claude Code hooks tclaw registers on the agent's
// subprocess. They run on tool events, so they are the only place a direct file
// write can be caught — an MCP tool cannot see one.
//
// The CLI calls them over HTTP on a loopback listener tclaw runs for each user.
// Every hook lets the call through when it has nothing to say, and rules-gate
// refuses a request it cannot read rather than guess.
package hooks

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// payload is the part of a hook's JSON input these hooks use.
type payload struct {
	ToolName  string    `json:"tool_name"`
	ToolInput toolInput `json:"tool_input"`

	// Prompt is what the user just sent, on UserPromptSubmit only.
	Prompt string `json:"prompt"`

	// SessionID ties a queued row back to the turn it came from.
	SessionID string `json:"session_id"`
}

type toolInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
}

// targetPath is the file a write tool is aimed at, or "" for a call that writes
// no file.
func (p payload) targetPath() string {
	if p.ToolInput.FilePath != "" {
		return p.ToolInput.FilePath
	}
	return p.ToolInput.NotebookPath
}

// Env is what a hook knows about where the turn runs.
type Env struct {
	MemoryDir string

	// ConfigDir is the CLI's config directory, home of the retro queue.
	ConfigDir string

	Channel string
}

// Outcome is what a hook decided. The zero value lets the call through silently.
type Outcome struct {
	// Refusal, when set, refuses the tool call and is the reason the agent reads.
	Refusal string

	// Context is guidance only the agent reads.
	Context string

	// Notice is one line the user sees in the chat, so a hook that acted is not
	// invisible to them. The CLI carries it as "<Event>:<Tool> says: <notice>".
	Notice string
}

// Evaluate runs the named hook on the CLI's JSON input. An unknown name passes: a stale
// registration must not start refusing tool calls.
func Evaluate(name string, env Env, input json.RawMessage) Outcome {
	var p payload
	if err := json.Unmarshal(input, &p); err != nil {
		if name == hookRulesGate {
			// A write the gate cannot read might be aimed at a rulebook.
			return refuse(env, blockParams{
				Guard:  hookRulesGate,
				Reason: fmt.Sprintf("Refused: the rules gate could not read this tool call (%v).", err),
			})
		}
		slog.Warn("failed to parse hook payload", "hook", name, "err", err)
		return Outcome{}
	}

	switch name {
	case hookRulesGate:
		return rulesGate(env, p)
	case hookRulesIndex:
		return rulesIndex(env, p)
	case hookLessonCapture:
		return lessonCapture(env, p)
	default:
		slog.Warn("unknown hook, passing", "hook", name)
		return Outcome{}
	}
}

// blockParams is what a refusal needs: the reason shown to the agent, and enough
// to file the row a later retro reads.
type blockParams struct {
	Guard     string
	SessionID string
	Reason    string
}

// refuse refuses the tool call and hands the agent the reason.
func refuse(env Env, params blockParams) Outcome {
	// Filed from here rather than at each call site: being stopped is the
	// evidence a retro reads, so no guard may leave it out.
	queueFeedback(env, feedbackEntry{
		SessionID: params.SessionID,
		Kind:      KindGuardBlock,
		Trigger:   params.Guard,
		Detail:    params.Reason,
	})
	// The CLI shows a refusal as "<Event>:<Tool> hook error: <reason>". Leading with the
	// guard's name in brackets is how the chat knows which hook refused.
	return Outcome{Refusal: fmt.Sprintf("[%s]: %s", params.Guard, params.Reason)}
}
