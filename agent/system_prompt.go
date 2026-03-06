package agent

import (
	"bytes"
	_ "embed"
	"log/slog"
	"text/template"
	"time"

	"tclaw/channel"
)

//go:embed system_prompt.md
var systemPromptRaw string

var systemPromptTmpl = template.Must(template.New("system_prompt").Parse(systemPromptRaw))

type systemPromptData struct {
	Date       string
	Channels   []channelInfo
	UserPrompt string
}

type channelInfo struct {
	Name        string
	Type        channel.ChannelType
	Description string
}

// BuildSystemPrompt executes the system_prompt.md template with runtime
// state and user config. The result is passed to --append-system-prompt.
func BuildSystemPrompt(channels map[channel.ChannelID]channel.Channel, userPrompt string) string {
	var chInfos []channelInfo
	for _, ch := range channels {
		info := ch.Info()
		chInfos = append(chInfos, channelInfo{
			Name:        info.Name,
			Type:        info.Type,
			Description: info.Description,
		})
	}

	data := systemPromptData{
		Date:       time.Now().Format("Monday, January 2, 2006"),
		Channels:   chInfos,
		UserPrompt: userPrompt,
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
// ~/.claude/CLAUDE.md when it doesn't exist yet.
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
