// Package user defines the User ID type and per-user Config struct. Pure data types with no I/O —
// system-derived paths (home dir, store path, socket path) are computed by the router at runtime.
package user

import (
	"time"

	"tclaw/internal/claudecli"
)

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

	// MessageDebounce coalesces same-channel user messages that arrive within
	// this rolling window into a single agent turn (e.g. a photo album delivered
	// as separate messages). 0 disables debouncing.
	MessageDebounce time.Duration

	// SystemPrompt is custom text appended after tclaw's default system prompt.
	// Configured per-user in tclaw.yaml.
	SystemPrompt string

	// TelegramUserID is the user's Telegram user ID from config. Used by the
	// Telegram provisioner for auto-start and notification delivery.
	TelegramUserID string

	// Knowledge configures the personal knowledge base, or nil if the user
	// has none.
	Knowledge *Knowledge
}

// Knowledge holds the resolved personal knowledge base settings for a user.
// Repo is a full clone URL; Branch defaults are applied during config load.
type Knowledge struct {
	Repo        string
	Branch      string
	CommitName  string
	CommitEmail string
}
