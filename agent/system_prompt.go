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
	Date          string
	Time          string
	Channels      []ChannelInfo
	HasLinks      bool
	DevSessions   []DevSessionInfo
	Notifications []NotificationInfo
	UserPrompt    string
	Onboarding    *OnboardingInfo
}

// NotificationInfo describes an active notification subscription for the system prompt.
type NotificationInfo struct {
	PackageName string
	TypeName    string
	Label       string
	ChannelName string
	Scope       string
}

// OnboardingInfo describes the current onboarding state for the system prompt.
// Nil means onboarding is complete or not applicable.
type OnboardingInfo struct {
	Phase        string
	InfoGathered map[string]bool
	InfoMissing  []string
	TipsShown    int
	TipsTotal    int
	NextTip      string

	// RemainingAreas lists feature areas not yet covered during tips phase.
	RemainingAreas []OnboardingFeatureArea
}

// OnboardingFeatureArea is a feature the agent can teach about.
type OnboardingFeatureArea struct {
	ID          string
	Name        string
	Description string
}

// ChannelLinkInfo describes a messaging link to/from another channel.
type ChannelLinkInfo struct {
	ChannelName string
	Description string
}

// ChannelInfo describes a channel for the system prompt template.
// Plain struct with no transport dependencies — the caller converts
// from channel.Info before calling BuildSystemPrompt.
type ChannelInfo struct {
	Name        string
	Type        string
	Description string
	Purpose     string // optional behavioral guidance for the agent on this channel
	Source      string // "static" or "dynamic"

	// OutboundLinks lists channels this channel can send messages to.
	OutboundLinks []ChannelLinkInfo

	// InboundLinks lists channels that can send messages to this channel.
	InboundLinks []ChannelLinkInfo
}

// DevSessionInfo describes an active dev session for the system prompt.
type DevSessionInfo struct {
	Branch      string
	WorktreeDir string
	Age         string

	// Stale is true when the session is older than the staleness threshold,
	// signaling that the agent should suggest cleanup rather than investigate.
	Stale bool
}

// BuildSystemPrompt executes the system_prompt.md template with runtime
// state and user config. The result is passed to --append-system-prompt.
func BuildSystemPrompt(channels []ChannelInfo, devSessions []DevSessionInfo, notifications []NotificationInfo, userPrompt string, onboarding *OnboardingInfo) string {
	hasLinks := false
	for _, ch := range channels {
		if len(ch.OutboundLinks) > 0 || len(ch.InboundLinks) > 0 {
			hasLinks = true
			break
		}
	}

	data := systemPromptData{
		Date:          time.Now().Format("Monday, January 2, 2006"),
		Time:          time.Now().Format("15:04 MST"),
		Channels:      channels,
		HasLinks:      hasLinks,
		DevSessions:   devSessions,
		Notifications: notifications,
		UserPrompt:    userPrompt,
		Onboarding:    onboarding,
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
