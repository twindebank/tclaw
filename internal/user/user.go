package user

import "tclaw/internal/claudecli"

// ID uniquely identifies a user across the system.
type ID string

// Config holds per-user settings for the agent and Claude CLI.
// System-derived values (home dir, store path, socket path) are not
// included here — the Router derives them from the base data directory.
type Config struct {
	ID              ID
	APIKey          string // ANTHROPIC_API_KEY for this user's claude sessions
	Model           claudecli.Model
	PermissionMode  claudecli.PermissionMode
	AllowedTools    []claudecli.Tool
	DisallowedTools []claudecli.Tool
	MaxTurns        int
	Debug           bool

	// SystemPrompt is custom text appended after tclaw's default system prompt.
	// Configured per-user in tclaw.yaml.
	SystemPrompt string

	// TelegramUserID is the user's Telegram user ID from config. Used by the
	// Telegram provisioner for auto-start and notification delivery.
	TelegramUserID string
}
