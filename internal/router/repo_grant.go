package router

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/repo"
)

// RepoGrantPayload describes the access change a pending repo_grant will apply.
// Carried in the pending action rather than re-derived on confirmation, so the
// user grants exactly what the prompt described even if the agent has moved on.
type RepoGrantPayload struct {
	// Repo is the tracked repo's name.
	Repo string `json:"repo"`

	// Access is the tier to grant.
	Access repo.Access `json:"access"`

	// Credential is the git credential slot label to authenticate with.
	Credential string `json:"credential,omitempty"`

	// DropToReadOnlyAt withdraws the grant when it passes. Zero means it stands
	// until changed.
	DropToReadOnlyAt time.Time `json:"drop_to_read_only_at,omitempty"`
}

// confirmRepoGrant applies a repo access grant the user has just confirmed.
//
// The tier is written to the tracked repo, which the git proxy consults on
// every request — so the grant takes effect on the next git operation without
// restarting anything.
func confirmRepoGrant(
	ctx context.Context,
	chID channel.ChannelID,
	chName string,
	pending channel.PendingAction,
	params confirmParams,
) bool {
	notify := func(text string) {
		if params.Notify != nil {
			params.Notify(ctx, chID, text)
		}
	}

	var payload RepoGrantPayload
	if err := json.Unmarshal(pending.Payload, &payload); err != nil {
		slog.Error("repo_grant: unreadable payload, grant not applied", "channel", chName, "err", err)
		notify("⚠️ Couldn't apply that access grant — the pending request was unreadable. Ask again.")
		return true
	}

	if params.RepoStore == nil {
		slog.Error("repo_grant: no repo store available", "channel", chName, "repo", payload.Repo)
		notify("⚠️ Couldn't apply that access grant — repo tracking is unavailable.")
		return true
	}

	tracked, err := params.RepoStore.Get(ctx, payload.Repo)
	if err != nil {
		slog.Error("repo_grant: failed to read tracked repo", "repo", payload.Repo, "err", err)
		notify(fmt.Sprintf("⚠️ Couldn't apply access for %q — failed to read its record.", payload.Repo))
		return true
	}
	if tracked == nil {
		// The repo was removed between the prompt and the reply.
		slog.Warn("repo_grant: repo no longer tracked", "repo", payload.Repo)
		notify(fmt.Sprintf("⚠️ %q is no longer tracked, so nothing was granted.", payload.Repo))
		return true
	}

	// Re-check visibility at confirmation time: a repo scoped away from this
	// channel since the prompt must not be granted from here.
	if !tracked.VisibleTo(chName) {
		slog.Warn("repo_grant: repo not visible on confirming channel", "repo", payload.Repo, "channel", chName)
		notify(fmt.Sprintf("⚠️ %q isn't available on this channel, so nothing was granted.", payload.Repo))
		return true
	}

	tracked.Access = payload.Access
	tracked.Credential = payload.Credential
	tracked.DropToReadOnlyAt = payload.DropToReadOnlyAt

	if err := params.RepoStore.Put(ctx, *tracked); err != nil {
		slog.Error("repo_grant: failed to save granted access", "repo", payload.Repo, "err", err)
		notify(fmt.Sprintf("⚠️ Couldn't save access for %q — nothing changed.", payload.Repo))
		return true
	}

	slog.Info("repo_grant: access granted", "repo", payload.Repo, "access", payload.Access,
		"channel", chName, "expires", payload.DropToReadOnlyAt)

	message := fmt.Sprintf("✅ %q is now %s.", payload.Repo, payload.Access)
	if !payload.DropToReadOnlyAt.IsZero() {
		message += fmt.Sprintf(" Reverts to read_only on %s.", payload.DropToReadOnlyAt.Format(time.RFC1123))
	}
	notify(message)
	return true
}
