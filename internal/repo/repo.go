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

	// Managed marks a repo declared in tclaw.yaml rather than added at runtime
	// via repo_add. The config is the source of truth for these: they are
	// re-provisioned on every boot and repo_remove refuses to delete them.
	Managed bool `json:"managed,omitempty"`

	// Channels restricts which channels may see this repo. Its clone is only
	// mounted for turns on a listed channel, and the repo tools refuse to
	// operate on it elsewhere. Empty means every channel.
	Channels []string `json:"channels,omitempty"`

	// Description is operator-supplied context from a config declaration,
	// surfaced to the agent by repo_list so it knows what the repo is for.
	Description string `json:"description,omitempty"`

	AddedAt      time.Time `json:"added_at"`
	LastSyncedAt time.Time `json:"last_synced_at,omitempty"`
}

// VisibleTo reports whether a turn on the named channel may see this repo.
// An unscoped repo is visible everywhere; a scoped one only on a listed
// channel. An unknown channel (empty name) fails closed against a scoped repo
// so a missing turn context can never widen access.
func (r TrackedRepo) VisibleTo(channelName string) bool {
	if len(r.Channels) == 0 {
		return true
	}
	for _, c := range r.Channels {
		if c == channelName {
			return true
		}
	}
	return false
}
