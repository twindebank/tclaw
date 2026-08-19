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

// block refuses the tool call and hands the agent the reason.
func block(reason string) {
	fmt.Fprintln(os.Stderr, reason)
	os.Exit(exitBlock)
}

// advise lets the call through and adds context the agent reads. PostToolUse
// output is only shown when it is valid JSON, so a marshal failure means saying
// nothing rather than printing a stray line into the transcript.
func advise(event, context string) {
	out, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     event,
			"additionalContext": context,
		},
	})
	if err != nil {
		slog.Error("failed to encode hook advice", "event", event, "err", err)
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
