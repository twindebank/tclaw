package repo

import "time"

// TrackedRepo represents a remote git repository being monitored for changes.
type TrackedRepo struct {
	Name string `json:"name"`
	URL  string `json:"url"`

	// Branch to track on the remote (e.g. "main").
	Branch string `json:"branch"`

	// LastSeenCommit is the SHA from the most recent sync. Empty before first sync.
	LastSeenCommit string `json:"last_seen_commit,omitempty"`

	// RepoDir is the absolute path to the bare repo cache on disk.
	RepoDir string `json:"repo_dir"`

	// WorktreeDir is the absolute path to the read-only checkout for file exploration.
	WorktreeDir string `json:"worktree_dir"`

	AddedAt      time.Time `json:"added_at"`
	LastSyncedAt time.Time `json:"last_synced_at,omitempty"`
}
