package agent

import (
	"bytes"
	_ "embed"
	"log/slog"
	"text/template"
	"time"
)

//go:embed system_prompt.md
var systemPromptRaw string

var systemPromptTmpl = template.Must(template.New("system_prompt").Parse(systemPromptRaw))

type systemPromptData struct {
	Date        string
	Channels    []ChannelInfo
	DevSessions []DevSessionInfo
	UserPrompt  string
}

// ChannelInfo describes a channel for the system prompt template.
// Plain struct with no transport dependencies — the caller converts
// from channel.Info before calling BuildSystemPrompt.
type ChannelInfo struct {
	Name        string
	Type        string
	Description string
	Source      string // "static" or "dynamic"
	Role        string // role name if set (e.g. "assistant", "developer")
}

// DevSessionInfo describes an active dev session for the system prompt.
type DevSessionInfo struct {
	Branch      string
	WorktreeDir string
	Age         string
}

// BuildSystemPrompt executes the system_prompt.md template with runtime
// state and user config. The result is passed to --append-system-prompt.
func BuildSystemPrompt(channels []ChannelInfo, devSessions []DevSessionInfo, userPrompt string) string {
	data := systemPromptData{
		Date:        time.Now().Format("Monday, January 2, 2006"),
		Channels:    channels,
		DevSessions: devSessions,
		UserPrompt:  userPrompt,
	}

	var buf bytes.Buffer
	if err := systemPromptTmpl.Execute(&buf, data); err != nil {
		// Template is embedded and tested at init — this shouldn't happen.
		slog.Error("failed to execute system prompt template", "err", err)
		return ""
	}
	return buf.String()
}

// DefaultMemoryTemplate is the initial content seeded into a new user's
// memory/CLAUDE.md when it doesn't exist yet. The file lives in the memory
// directory (the agent's CWD), so all paths are relative to that directory.
const DefaultMemoryTemplate = `# Memory

This is your persistent memory. It is loaded into every conversation automatically.
Update it to remember things across sessions.

For topic-specific knowledge, create separate files in this directory and reference
them from here using @filename.md syntax. The CLI will auto-load referenced files.

Use subfiles for knowledge that's only relevant in certain contexts — this keeps
the main memory file concise and avoids bloating every conversation with niche details.

## User Preferences
(none yet)

## Topic Files
(none yet — create files like @coding-preferences.md, @project-notes.md, etc.)

## Notes
(none yet)
`
