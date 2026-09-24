package hooks

import (
	"encoding/json"
	"fmt"
)

// BinaryName is the hook binary as installed in the image.
const BinaryName = "tclaw-hooks"

// HookEvent is one of Claude Code's hook events.
type HookEvent string

const (
	eventPreToolUse       HookEvent = "PreToolUse"
	eventPostToolUse      HookEvent = "PostToolUse"
	eventUserPromptSubmit HookEvent = "UserPromptSubmit"
)

// HookSpec is one registration. Matchers name the tools the hook needs to see:
// a hook checks the target itself, so a wide matcher only costs a process start,
// but every tool call pays that.
type HookSpec struct {
	Name    string
	Event   HookEvent
	Matcher string
}

// writeTools are the tools that can put content into a file.
const writeTools = "Write|Edit|MultiEdit|NotebookEdit"

// Manifest is the one catalogue of hooks tclaw registers. The settings block is
// built from it, so a hook cannot be implemented and left unregistered, or
// registered under an event it never sees.
var Manifest = []HookSpec{
	{"rules-gate", eventPreToolUse, writeTools},
	{"rules-index", eventPostToolUse, writeTools},
	// No matcher: a prompt is not a tool call, so this one sees every turn.
	{"lesson-capture", eventUserPromptSubmit, ""},
}

// SettingsBlock builds the "hooks" value for a user's settings.json, pointing
// every hook at binary. The path is written out in full rather than left to the
// environment: hooks run under a shell that reads no profile, so a command
// relying on a variable runs nothing and fails on every tool call.
func SettingsBlock(binary string) (json.RawMessage, error) {
	if binary == "" {
		return nil, fmt.Errorf("no hook binary path")
	}
	type command struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type group struct {
		Matcher string    `json:"matcher,omitempty"`
		Hooks   []command `json:"hooks"`
	}
	events := map[HookEvent][]group{}
	for _, hook := range Manifest {
		events[hook.Event] = append(events[hook.Event], group{
			Matcher: hook.Matcher,
			Hooks:   []command{{Type: "command", Command: fmt.Sprintf("%q %s", binary, hook.Name)}},
		})
	}
	raw, err := json.Marshal(events)
	if err != nil {
		return nil, fmt.Errorf("encode hooks block: %w", err)
	}
	return raw, nil
}
