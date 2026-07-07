package router

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/channel/outbox"
)

// knowledgeSyncTimeout bounds a single background sync so a stalled git
// command or network call can't accumulate indefinitely across turns.
const knowledgeSyncTimeout = 90 * time.Second

// knowledgeSyncParams holds the dependencies for one background vault sync.
type knowledgeSyncParams struct {
	Dir          string
	UserID       string
	ChannelName  string
	Outbox       *outbox.Outbox
	ChannelsFunc func() map[channel.ChannelID]channel.Channel
}

// syncKnowledgeVault commits any pending edits, rebases onto the remote, and
// pushes — run in the background after every turn so the vault stays in sync
// without the agent needing to run git itself. Best-effort, like the rest of
// the knowledge base wiring (see provisionKnowledgeClone): failures are
// logged, never fatal, and never re-engage the agent. The one case worth
// surfacing to the user is a rebase conflict, since it needs a human (or a
// deliberate future turn) to resolve — that's delivered as a notification-only
// message via the outbox rather than a queued turn.
func syncKnowledgeVault(ctx context.Context, p knowledgeSyncParams) {
	if _, err := os.Stat(filepath.Join(p.Dir, ".git")); err != nil {
		// Not cloned (provisioning failed earlier and already logged) — nothing to sync.
		return
	}

	ctx, cancel := context.WithTimeout(ctx, knowledgeSyncTimeout)
	defer cancel()

	dirty, err := gitIsDirty(ctx, p.Dir)
	if err != nil {
		slog.Error("knowledge sync: status check failed", "user", p.UserID, "err", err)
		return
	}
	if dirty {
		if err := gitCommitAll(ctx, p.Dir); err != nil {
			slog.Error("knowledge sync: commit failed", "user", p.UserID, "err", err)
			return
		}
	}

	if err := gitPullRebase(ctx, p.Dir); err != nil {
		if !gitRebaseInProgress(p.Dir) {
			slog.Error("knowledge sync: pull --rebase failed", "user", p.UserID, "err", err)
			return
		}
		// Conflict: back out to the pre-rebase state (local commit stays intact,
		// just unpushed) rather than leaving the clone mid-rebase for the next
		// turn to trip over.
		if abortErr := gitRebaseAbort(ctx, p.Dir); abortErr != nil {
			slog.Error("knowledge sync: rebase abort failed", "user", p.UserID, "err", abortErr)
		}
		notifyKnowledgeConflict(ctx, p)
		return
	}

	ahead, err := gitIsAhead(ctx, p.Dir)
	if err != nil {
		slog.Error("knowledge sync: ahead check failed", "user", p.UserID, "err", err)
		return
	}
	if !ahead {
		return
	}

	if err := gitPush(ctx, p.Dir); err != nil {
		slog.Error("knowledge sync: push failed", "user", p.UserID, "err", err)
	}
}

// notifyKnowledgeConflict sends a one-off alert to the channel whose turn
// triggered the sync. Delivered via the outbox — straight to the transport —
// rather than the inbound queue, so reporting the conflict doesn't itself cost
// a full agent turn.
func notifyKnowledgeConflict(ctx context.Context, p knowledgeSyncParams) {
	chID := resolveChannelID(p.ChannelsFunc, p.ChannelName)
	if chID == "" {
		slog.Warn("knowledge sync: could not resolve channel for conflict alert", "channel", p.ChannelName)
		return
	}
	message := fmt.Sprintf(
		"⚠️ Knowledge vault auto-sync hit a rebase conflict with the remote. "+
			"Your local commit is intact but unpushed. Resolve manually: cd %s && git pull --rebase, "+
			"fix the conflicts, then git push.",
		p.Dir,
	)
	if _, err := p.Outbox.Send(ctx, chID, message, channel.SendOpts{Notify: true}); err != nil {
		slog.Error("knowledge sync: failed to send conflict alert", "channel", p.ChannelName, "err", err)
	}
}

// --- git helpers ---

func gitIsDirty(ctx context.Context, dir string) (bool, error) {
	out, err := runGit(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func gitCommitAll(ctx context.Context, dir string) error {
	if _, err := runGit(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	_, err := runGit(ctx, dir, "commit", "-m", "Auto-sync from tclaw")
	return err
}

func gitPullRebase(ctx context.Context, dir string) error {
	_, err := runGit(ctx, dir, "pull", "--rebase")
	return err
}

// gitRebaseInProgress reports whether dir is currently mid-rebase, which is
// how a failed gitPullRebase is distinguished from an unrelated failure (e.g.
// the proxy being briefly unreachable) — only the former needs an abort.
func gitRebaseInProgress(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git", "rebase-merge")); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Join(dir, ".git", "rebase-apply"))
	return err == nil
}

func gitRebaseAbort(ctx context.Context, dir string) error {
	_, err := runGit(ctx, dir, "rebase", "--abort")
	return err
}

func gitIsAhead(ctx context.Context, dir string) (bool, error) {
	out, err := runGit(ctx, dir, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		return false, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return false, fmt.Errorf("parse rev-list count %q: %w", out, err)
	}
	return count > 0, nil
}

func gitPush(ctx context.Context, dir string) error {
	_, err := runGit(ctx, dir, "push")
	return err
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}
