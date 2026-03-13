package user

import (
	"tclaw/claudecli"
	"tclaw/role"
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

	// Role is a named preset of tool permissions. Mutually exclusive with
	// AllowedTools — set one or the other. Applies as the default for
	// channels that don't specify their own role or allowed_tools.
	Role role.Role

	// SystemPrompt is custom text appended after tclaw's default system prompt.
	// Configured per-user in tclaw.yaml.
	SystemPrompt string
}
