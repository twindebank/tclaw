package router

import (
	"context"
	"log/slog"
	"os"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/repo"
	"tclaw/internal/tool/repotools"
)

// repoSweepInterval is how often lifecycle rules are applied. Grants and idle
// clones are measured in hours or days, so checking every few minutes is
// responsive enough without churning the store.
const repoSweepInterval = 5 * time.Minute

// repoSweepParams holds inputs for the repo lifecycle sweep.
type repoSweepParams struct {
	Store     *repo.Store
	RemoteURL func(name string) string

	// Notify reports a withdrawn grant to the channels that could see the repo,
	// so push access never lapses silently mid-task.
	Notify func(ctx context.Context, channelNames []string, text string)
}

// sweepRepoLifecycles applies the time-based repo rules until ctx is done:
// expired grants drop to read-only, unused clones are removed from disk, and
// repos with a fetch interval are refreshed.
//
// Runs for the lifetime of the user, not the agent, so a grant expires whether
// or not a turn is in flight.
func sweepRepoLifecycles(ctx context.Context, params repoSweepParams) {
	ticker := time.NewTicker(repoSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runRepoSweep(ctx, params)
		}
	}
}

// runRepoSweep is one pass of the lifecycle rules. Errors on one repo are
// logged and the rest continue: a sweep is best-effort maintenance and must
// never wedge on a single bad entry.
func runRepoSweep(ctx context.Context, params repoSweepParams) {
	repos, err := params.Store.List(ctx)
	if err != nil {
		slog.Error("repo sweep: failed to list tracked repos", "err", err)
		return
	}

	now := time.Now()
	for _, tracked := range repos {
		if expireGrant(ctx, tracked, now, params) {
			// The entry was rewritten; the remaining rules can wait for the
			// next pass rather than acting on a stale copy.
			continue
		}
		dropUnusedClone(ctx, tracked, now, params)
		refreshRepo(ctx, tracked, now, params)
	}
}

// expireGrant withdraws push access whose window has passed, reporting it to
// the channels that can see the repo. Returns whether the repo was changed.
func expireGrant(ctx context.Context, tracked repo.TrackedRepo, now time.Time, params repoSweepParams) bool {
	if tracked.DropToReadOnlyAt.IsZero() || now.Before(tracked.DropToReadOnlyAt) {
		return false
	}
	if !tracked.Access.AllowsPush() {
		return false
	}

	// The clone and its history stay: only the capability is withdrawn.
	tracked.Access = repo.AccessReadOnly
	tracked.DropToReadOnlyAt = time.Time{}
	if err := params.Store.Put(ctx, tracked); err != nil {
		slog.Error("repo sweep: failed to withdraw expired grant", "repo", tracked.Name, "err", err)
		return false
	}

	slog.Info("repo sweep: access grant expired", "repo", tracked.Name)
	if params.Notify != nil {
		params.Notify(ctx, tracked.Channels,
			"🔒 Push access to "+tracked.Name+" has expired — it is read-only again. "+
				"Ask for it again if you still need it.")
	}
	return true
}

// dropUnusedClone removes a clone that has sat unused, freeing disk. The store
// entry survives, so the next sync recreates it.
func dropUnusedClone(ctx context.Context, tracked repo.TrackedRepo, now time.Time, params repoSweepParams) {
	if tracked.DropCloneIfUnusedFor <= 0 {
		return
	}
	// A repo that has never been used measures from when it was added, so a
	// clone nothing ever touches is still eventually reclaimed.
	lastUsed := tracked.LastUsedAt
	if lastUsed.IsZero() {
		lastUsed = tracked.AddedAt
	}
	if lastUsed.IsZero() || now.Sub(lastUsed) < tracked.DropCloneIfUnusedFor {
		return
	}
	if _, err := os.Stat(tracked.RepoDir); err != nil {
		return
	}

	if err := os.RemoveAll(tracked.RepoDir); err != nil {
		slog.Error("repo sweep: failed to drop unused clone", "repo", tracked.Name, "err", err)
		return
	}
	slog.Info("repo sweep: dropped unused clone", "repo", tracked.Name,
		"unused_for", now.Sub(lastUsed).Truncate(time.Hour))
}

// refreshRepo re-fetches a repo whose fetch interval has elapsed, so a mirror
// the agent relies on doesn't quietly go stale.
func refreshRepo(ctx context.Context, tracked repo.TrackedRepo, now time.Time, params repoSweepParams) {
	if tracked.FetchEvery <= 0 {
		return
	}
	if !tracked.LastSyncedAt.IsZero() && now.Sub(tracked.LastSyncedAt) < tracked.FetchEvery {
		return
	}
	if _, err := os.Stat(tracked.RepoDir); err != nil {
		// The clone was dropped as unused; leave it for repo_sync to recreate
		// rather than resurrecting something nothing is asking for.
		return
	}

	if err := repotools.CloneOrFetch(repotools.CloneParams{
		RepoDir:       tracked.RepoDir,
		URL:           params.RemoteURL(tracked.Name),
		Branch:        tracked.Branch,
		Depth:         configRepoSyncDepth,
		ResetToRemote: !tracked.EffectiveAccess(now).AllowsPush(),
	}); err != nil {
		slog.Warn("repo sweep: background fetch failed", "repo", tracked.Name, "err", err)
		return
	}

	// Only the sync timestamp moves: LastSeenCommit stays put so the next
	// repo_sync still reports everything new since the agent last looked.
	tracked.LastSyncedAt = now
	if err := params.Store.Put(ctx, tracked); err != nil {
		slog.Error("repo sweep: failed to record background fetch", "repo", tracked.Name, "err", err)
	}
}

// notifyRepoChannels delivers a repo lifecycle message to the channels that can
// see the repo. An unscoped repo (no channel list) is reported everywhere,
// since any channel might have been relying on the access.
func notifyRepoChannels(
	ctx context.Context,
	channelNames []string,
	text string,
	channels func() map[channel.ChannelID]channel.Channel,
	send func(context.Context, channel.ChannelID, string, channel.SendOpts) (channel.MessageID, error),
) {
	allowed := make(map[string]bool, len(channelNames))
	for _, name := range channelNames {
		allowed[name] = true
	}

	for chID, ch := range channels() {
		if len(allowed) > 0 && !allowed[ch.Info().Name] {
			continue
		}
		if _, err := send(ctx, chID, text, channel.SendOpts{}); err != nil {
			slog.Error("repo sweep: failed to report lifecycle change",
				"channel", ch.Info().Name, "err", err)
		}
	}
}
