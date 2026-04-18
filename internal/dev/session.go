// Package dev provides types and a store for dev workflow sessions. Tracks active git worktree
// sessions, cached repo URLs, GitHub tokens, and the currently deployed commit hash. Used by
// dev_start, dev_end, dev_status, and deploy MCP tools.
package dev

import "time"

// SessionStatus represents the state of a dev session.
type SessionStatus string

const (
	SessionActive SessionStatus = "active"
)

// Session tracks a single dev worktree session.
type Session struct {
	Branch      string        `json:"branch"`
	WorktreeDir string        `json:"worktree_dir"`
	RepoDir     string        `json:"repo_dir"`
	Status      SessionStatus `json:"status"`
	CreatedAt   time.Time     `json:"created_at"`

	// CreatedByChannel is the name of the channel that invoked dev_start.
	// Ephemeral channel cleanup uses this to delete sessions bound to an
	// ephemeral channel when it's torn down; the field is empty when the
	// session wasn't started from a specific channel (e.g. stdio / tests).
	CreatedByChannel string `json:"created_by_channel,omitempty"`
}
