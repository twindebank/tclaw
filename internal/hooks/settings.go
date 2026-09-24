package hooks

import (
	"encoding/json"
	"fmt"

	"tclaw/internal/memorylayout"
)

// HookEvent is one of Claude Code's hook events.
type HookEvent string

const (
	eventPreToolUse       HookEvent = "PreToolUse"
	eventPostToolUse      HookEvent = "PostToolUse"
	eventUserPromptSubmit HookEvent = "UserPromptSubmit"
)

const (
	hookRulesGate     = "rules-gate"
	hookRulesIndex    = "rules-index"
	hookLessonCapture = "lesson-capture"
)

// HookSpec is one registration. Matchers name the tools the hook needs to see:
// a hook checks the target itself, so a wide matcher only costs a request, but
// every tool call pays that.
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
	{hookRulesGate, eventPreToolUse, writeTools},
	{hookRulesIndex, eventPostToolUse, writeTools},
	// No matcher: a prompt is not a tool call, so this one sees every turn.
	{hookLessonCapture, eventUserPromptSubmit, ""},
}

// IsHookName reports whether name is one of tclaw's own hooks.
func IsHookName(name string) bool {
	_, ok := specFor(name)
	return ok
}

func specFor(name string) (HookSpec, bool) {
	for _, spec := range Manifest {
		if spec.Name == name {
			return spec, true
		}
	}
	return HookSpec{}, false
}

// headerChannel carries the turn's channel name, filled in by the CLI from the environment.
const headerChannel = "X-Tclaw-Channel"

// hookTimeoutSeconds bounds one hook request. The hooks read a few small files at most.
const hookTimeoutSeconds = 30

// SettingsBlock builds the "hooks" value for a user's settings.json, pointing every hook at
// the user's hook server at baseURL. The CLI fills the token and channel headers in from the
// turn's environment, which only tclaw sets.
func SettingsBlock(baseURL string) (json.RawMessage, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("no hook server URL")
	}
	type httpHook struct {
		Type           string            `json:"type"`
		URL            string            `json:"url"`
		Timeout        int               `json:"timeout"`
		Headers        map[string]string `json:"headers"`
		AllowedEnvVars []string          `json:"allowedEnvVars"`
	}
	type group struct {
		Matcher string     `json:"matcher,omitempty"`
		Hooks   []httpHook `json:"hooks"`
	}
	events := map[HookEvent][]group{}
	for _, hook := range Manifest {
		events[hook.Event] = append(events[hook.Event], group{
			Matcher: hook.Matcher,
			Hooks: []httpHook{{
				Type:    "http",
				URL:     baseURL + hookPathPrefix + hook.Name,
				Timeout: hookTimeoutSeconds,
				Headers: map[string]string{
					"Authorization": "Bearer $" + memorylayout.EnvHookToken,
					headerChannel:   "$" + memorylayout.EnvChannel,
				},
				AllowedEnvVars: []string{memorylayout.EnvHookToken, memorylayout.EnvChannel},
			}},
		})
	}
	raw, err := json.Marshal(events)
	if err != nil {
		return nil, fmt.Errorf("encode hooks block: %w", err)
	}
	return raw, nil
}
