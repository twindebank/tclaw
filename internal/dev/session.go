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
}
