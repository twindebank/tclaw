// Package user defines the User ID type and per-user Config struct. Pure data types with no I/O —
// system-derived paths (home dir, store path, socket path) are computed by the router at runtime.
package user

import (
	"time"

	"tclaw/internal/claudecli"
	"tclaw/internal/repo"
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

	// Repos are git repositories declared in config, cloned on boot and
	// mounted read-only for the agent to browse.
	Repos []Repo
}

// Repo is a config-declared git repository the agent can browse. Unlike the
// knowledge base it is a read-only mirror: the clone is refreshed from the
// remote and any local edit is discarded on the next sync.
type Repo struct {
	// Name is the directory alias under <user>/repos/ and the handle the repo
	// tools take.
	Name string

	// URL is the full HTTPS clone URL, expanded from config shorthand.
	URL string

	// Branch is the branch tracked on the remote.
	Branch string

	// Description tells the agent what the repo is for.
	Description string

	// Channels restricts the repo to the named channels. Empty means all.
	Channels []string

	// Access is what the agent may do with the remote.
	Access repo.Access

	// Credential is the git credential slot label authenticating this repo.
	// Empty uses the default slot.
	Credential string

	// FetchEvery refreshes the clone in the background at this interval.
	// Zero means it only refreshes on repo_sync.
	FetchEvery time.Duration

	// DropToReadOnlyAt withdraws push access at this instant. Zero never expires.
	DropToReadOnlyAt time.Time

	// DropCloneIfUnusedFor removes the clone from disk after this long unused.
	// Zero keeps it indefinitely.
	DropCloneIfUnusedFor time.Duration
}

// Knowledge holds the resolved personal knowledge base settings for a user.
// Repo is a full clone URL; Branch defaults are applied during config load.
type Knowledge struct {
	Repo        string
	Branch      string
	CommitName  string
	CommitEmail string
}
