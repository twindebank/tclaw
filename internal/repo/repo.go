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

	// Credential is the label of the git credential slot used to authenticate
	// this repo. Empty means the default slot, so a repo needs its own entry
	// here only when it should be scoped to a narrower token.
	Credential string `json:"credential,omitempty"`

	// Access is what the agent may do with the remote. Empty means read-only,
	// so a repo that predates tiers, or one added without asking for more,
	// gets the least privilege.
	Access Access `json:"access,omitempty"`

	// DropToReadOnlyAt withdraws push access when it passes. The repo and its
	// clone stay; only the tier drops. Zero means the grant does not expire.
	DropToReadOnlyAt time.Time `json:"drop_to_read_only_at,omitempty"`

	// DropCloneIfUnusedFor removes the clone from disk after this long without
	// use. Disk hygiene only — the entry survives and the clone is recreated on
	// the next sync. Zero disables it.
	DropCloneIfUnusedFor time.Duration `json:"drop_clone_if_unused_for,omitempty"`

	// FetchEvery is how often the background sweep refreshes the clone.
	// Zero means it is only refreshed when repo_sync is called.
	FetchEvery time.Duration `json:"fetch_every,omitempty"`

	// LastUsedAt is when a tool last read or synced this repo, which is what
	// DropCloneIfUnusedFor measures against.
	LastUsedAt time.Time `json:"last_used_at,omitempty"`

	AddedAt      time.Time `json:"added_at"`
	LastSyncedAt time.Time `json:"last_synced_at,omitempty"`
}

// EffectiveAccess returns the tier in force, treating an unset tier as
// read-only and an elapsed grant as expired.
func (r TrackedRepo) EffectiveAccess(now time.Time) Access {
	if r.Access == "" {
		return AccessReadOnly
	}
	if !r.DropToReadOnlyAt.IsZero() && now.After(r.DropToReadOnlyAt) {
		return AccessReadOnly
	}
	return r.Access
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

// Access is what the agent may do with a repo's remote. Enforced by the git
// proxy at the transport layer rather than by the tools, because the agent runs
// git itself and anything checked tool-side could be bypassed with raw git.
type Access string

const (
	// AccessReadOnly permits fetching only. The clone is mounted read-only.
	AccessReadOnly Access = "read_only"

	// AccessPullRequestsOnly permits pushing any branch except the default one,
	// so changes can only reach the default branch through a reviewed PR.
	AccessPullRequestsOnly Access = "pull_requests_only"

	// AccessFullWrite permits pushing anywhere, including the default branch.
	AccessFullWrite Access = "full_write"
)

// ValidAccess reports whether a is a known access tier.
func ValidAccess(a Access) bool {
	switch a {
	case AccessReadOnly, AccessPullRequestsOnly, AccessFullWrite:
		return true
	default:
		return false
	}
}

// ValidAccessTiers lists the tiers, for error messages and tool schemas.
func ValidAccessTiers() []Access {
	return []Access{AccessReadOnly, AccessPullRequestsOnly, AccessFullWrite}
}

// AllowsPush reports whether the tier permits any push at all.
func (a Access) AllowsPush() bool {
	return a == AccessPullRequestsOnly || a == AccessFullWrite
}

// Exceeds reports whether a grants more than other, which is what makes a
// change a grant needing confirmation rather than a downgrade.
func (a Access) Exceeds(other Access) bool {
	return accessRank(a) > accessRank(other)
}

func accessRank(a Access) int {
	switch a {
	case AccessPullRequestsOnly:
		return 1
	case AccessFullWrite:
		return 2
	default:
		return 0
	}
}
