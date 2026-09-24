// Package hooks implements the Claude Code hooks tclaw registers on the agent's
// subprocess. They run inside the sandbox on tool events, so they are the only
// place a direct file write can be caught — an MCP tool cannot see one.
//
// Every hook fails open. They run on every tool call, so a hook that errors on
// unexpected input would wedge the whole turn; the deliberate block is the one
// non-zero exit that is meant.
package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// exitBlock is the exit code Claude Code reads as "refuse this tool call and
// show the agent what was written to stderr".
const exitBlock = 2

// payload is the part of a hook's stdin JSON these hooks use.
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

// Run dispatches the hook named in args. An unknown name passes: a stale
// registration must not start refusing tool calls.
func Run(args []string) {
	if len(args) < 2 {
		pass()
	}
	switch args[1] {
	case "rules-gate":
		rulesGate()
	case "rules-index":
		rulesIndex()
	case "lesson-capture":
		lessonCapture()
	default:
		slog.Warn("unknown hook, passing", "hook", args[1])
		pass()
	}
	pass()
}

// pass lets the tool call through.
func pass() {
	os.Exit(0)
}

// blockParams is what a refusal needs: the reason shown to the agent, and enough
// to file the row a later retro reads.
type blockParams struct {
	Guard     string
	SessionID string
	Reason    string
}

// block refuses the tool call and hands the agent the reason.
func block(params blockParams) {
	// Filed from here rather than at each call site: being stopped is the
	// evidence a retro reads, so no guard may leave it out.
	queueFeedback(feedbackEntry{
		SessionID: params.SessionID,
		Kind:      KindGuardBlock,
		Trigger:   params.Guard,
		Detail:    params.Reason,
	})
	fmt.Fprintln(os.Stderr, params.Reason)
	os.Exit(exitBlock)
}

// advice is what a hook hands back when it lets a call through.
type advice struct {
	Event HookEvent

	// Context is guidance only the agent reads.
	Context string

	// Notice is one line the user sees in the chat, so a hook that acted is not
	// invisible to them. The CLI carries it as "<Event>:<Tool> says: <notice>".
	Notice string
}

// advise lets the call through and hands back the advice. Output is only shown
// when it is valid JSON, so a marshal failure means saying nothing rather than
// printing a stray line into the transcript.
func advise(a advice) {
	body := map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     string(a.Event),
			"additionalContext": a.Context,
		},
	}
	if a.Notice != "" {
		// An empty one would still be a key the CLI has to decide to ignore.
		body["systemMessage"] = a.Notice
	}
	out, err := json.Marshal(body)
	if err != nil {
		slog.Error("failed to encode hook advice", "event", a.Event, "err", err)
		pass()
	}
	fmt.Println(string(out))
	pass()
}

// readPayload parses the hook's stdin. Anything unreadable returns an empty
// payload, which every hook treats as "nothing to act on".
func readPayload() payload {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		slog.Warn("failed to read hook payload", "err", err)
		return payload{}
	}
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		slog.Warn("failed to parse hook payload", "err", err)
		return payload{}
	}
	return p
}
