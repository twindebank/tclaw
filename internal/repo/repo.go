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

	// RepoDir is the absolute path to the local clone on disk. This is a
	// regular (non-bare) clone — the agent can both browse files and run
	// git commands (log, diff, blame) directly in it.
	RepoDir string `json:"repo_dir"`

	// WorktreeDir is unused since the switch from bare+worktree to a single
	// non-bare clone. Kept for backwards compatibility with persisted state
	// from before the migration — new repos leave it empty.
	WorktreeDir string `json:"worktree_dir,omitempty"`

	AddedAt      time.Time `json:"added_at"`
	LastSyncedAt time.Time `json:"last_synced_at,omitempty"`
}
