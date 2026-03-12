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
