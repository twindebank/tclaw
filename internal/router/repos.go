package router

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/credential"
	"tclaw/internal/gitproxy"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/repo"
	"tclaw/internal/tool/repotools"
	"tclaw/internal/user"
)

// configRepoSyncDepth is the history fetched for config-declared repos on boot.
// Deep enough for the agent to answer "what changed recently" without pulling
// the full history of a long-lived repo.
const configRepoSyncDepth = 50

// reposProvisionParams holds inputs for provisioning config-declared repos.
type reposProvisionParams struct {
	UserID  user.ID
	UserDir string
	Repos   []user.Repo
	Store   *repo.Store

	// RemoteURL returns the proxy remote a clone should point at. Every clone
	// is fetched through the proxy, including tclaw's own, so there is one
	// authentication path and no credential in any .git/config.
	RemoteURL func(name string) string
}

// provisionConfigRepos reconciles the tracked-repo store against the repos
// declared in tclaw.yaml, then clones or refreshes each one. Config is the
// source of truth for declared repos: they are re-registered on every boot (so
// they survive a volume wipe) and dropped when removed from the config.
//
// Repos the agent added itself via repo_add are left untouched.
//
// A failure on one repo is logged and the rest continue — a repo that can't be
// cloned (network, revoked token, renamed remote) must not take down the user
// session, and the agent can retry with repo_sync.
func provisionConfigRepos(ctx context.Context, params reposProvisionParams) error {
	tracked, err := params.Store.List(ctx)
	if err != nil {
		return fmt.Errorf("list tracked repos: %w", err)
	}

	declared := make(map[string]bool, len(params.Repos))
	for _, r := range params.Repos {
		declared[r.Name] = true
	}

	// Drop managed entries that are no longer declared. Their clones were owned
	// by the config, so the directory goes with them.
	for name, t := range tracked {
		if !t.Managed || declared[name] {
			continue
		}
		slog.Info("removing repo dropped from config", "user", params.UserID, "repo", name)
		if err := os.RemoveAll(t.RepoDir); err != nil {
			slog.Error("failed to remove clone of undeclared repo", "user", params.UserID, "repo", name, "err", err)
		}
		if err := params.Store.Delete(ctx, name); err != nil {
			slog.Error("failed to delete undeclared repo from store", "user", params.UserID, "repo", name, "err", err)
		}
	}

	for _, r := range params.Repos {
		repoDir := repoCloneDir(params.UserDir, r)
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			slog.Error("failed to create repo dir", "user", params.UserID, "repo", r.Name, "err", err)
			continue
		}

		// Preserve the sync cursor across restarts so repo_sync keeps reporting
		// only what's new, rather than replaying recent history every boot.
		entry := repo.TrackedRepo{
			Name:                 r.Name,
			URL:                  r.URL,
			Branch:               r.Branch,
			RepoDir:              repoDir,
			Managed:              true,
			Channels:             r.Channels,
			Description:          r.Description,
			Access:               r.Access,
			Credential:           r.Credential,
			FetchEvery:           r.FetchEvery,
			DropToReadOnlyAt:     r.DropToReadOnlyAt,
			DropCloneIfUnusedFor: r.DropCloneIfUnusedFor,
			AddedAt:              time.Now(),
		}
		if existing, ok := tracked[r.Name]; ok {
			entry.LastSeenCommit = existing.LastSeenCommit
			entry.LastSyncedAt = existing.LastSyncedAt
			entry.LastUsedAt = existing.LastUsedAt
			entry.AddedAt = existing.AddedAt
		}
		if err := params.Store.Put(ctx, entry); err != nil {
			slog.Error("failed to register config repo", "user", params.UserID, "repo", r.Name, "err", err)
			continue
		}

		if err := repotools.CloneOrFetch(repotools.CloneParams{
			RepoDir:       repoDir,
			URL:           params.RemoteURL(r.Name),
			Branch:        r.Branch,
			Depth:         configRepoSyncDepth,
			ResetToRemote: !entry.EffectiveAccess(time.Now()).AllowsPush(),
		}); err != nil {
			slog.Error("failed to clone config repo", "user", params.UserID, "repo", r.Name, "err", err)
			continue
		}
		slog.Info("config repo ready", "user", params.UserID, "repo", r.Name,
			"dir", repoDir, "access", entry.Access, "channels", r.Channels)
	}

	return nil
}

// repoCloneDir is where a declared repo is cloned. Most land under repos/;
// the knowledge vault overrides this so it keeps the path the system prompt
// and the knowledge skill refer to.
func repoCloneDir(userDir string, r user.Repo) string {
	if r.MountAt != "" {
		return filepath.Join(userDir, r.MountAt)
	}
	return filepath.Join(userDir, "repos", r.Name)
}

// channelName resolves a channel ID to its configured name, which is what repo
// scoping is expressed in. Returns "" for an unknown ID, which fails closed:
// scoped repos stay hidden.
func channelName(channels map[channel.ChannelID]channel.Channel, chID channel.ChannelID) string {
	ch, ok := channels[chID]
	if !ok {
		slog.Warn("unknown channel for repo scoping, hiding scoped repos", "channel_id", chID)
		return ""
	}
	return ch.Info().Name
}

// repoMounts is the set of repo clone directories relevant to a single turn,
// split by how each must be bound into the sandbox.
type repoMounts struct {
	// Visible clones are readable by this channel and passed as --add-dir.
	Visible []string

	// ReadOnly clones are bound read-only: they are mirrors that repo_sync
	// resets to the remote, so an edit would be silently discarded. A repo the
	// agent may push from is writable, since committing needs a writable clone.
	ReadOnly []string

	// Masked clones belong to other channels and are hidden behind a tmpfs.
	Masked []string
}

// resolveRepoMounts splits the tracked repos into those the named channel may
// see and those it may not. Called per turn, so a repo added or scoped since
// the last turn is reflected immediately.
//
// Directories that don't exist on disk are skipped entirely — a store entry can
// outlive its clone (volume wipe), and bwrap fails the whole turn on a missing
// bind or tmpfs target. There is nothing to expose or hide either way.
//
// On a store error nothing is reported visible and nothing masked: the turn
// proceeds without repo mounts rather than with an unscoped view.
func resolveRepoMounts(ctx context.Context, store *repo.Store, channelName string) repoMounts {
	repos, err := store.List(ctx)
	if err != nil {
		slog.Error("failed to list tracked repos for mounts", "channel", channelName, "err", err)
		return repoMounts{}
	}

	now := time.Now()
	var mounts repoMounts
	for _, r := range repos {
		if _, statErr := os.Stat(r.RepoDir); statErr != nil {
			slog.Warn("skipping mount for tracked repo with no clone on disk",
				"repo", r.Name, "dir", r.RepoDir, "err", statErr)
			continue
		}
		if !r.VisibleTo(channelName) {
			mounts.Masked = append(mounts.Masked, r.RepoDir)
			continue
		}
		mounts.Visible = append(mounts.Visible, r.RepoDir)
		if !r.EffectiveAccess(now).AllowsPush() {
			mounts.ReadOnly = append(mounts.ReadOnly, r.RepoDir)
		}
	}
	return mounts
}

// newGitProxyLookup resolves a tracked repo name to what the proxy needs to
// serve it: the upstream path, the tier in force, and the token from the repo's
// credential slot.
//
// Called per request, so a tier granted mid-session or a token filled from a
// phone takes effect on the next git operation without a restart.
func newGitProxyLookup(repoStore *repo.Store, secretStore secret.Store) gitproxy.Lookup {
	return func(ctx context.Context, name string) (*gitproxy.Repo, error) {
		tracked, err := repoStore.Get(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("look up repo %q: %w", name, err)
		}
		if tracked == nil {
			return nil, nil
		}

		path, err := repoPathFromURL(tracked.URL)
		if err != nil {
			return nil, fmt.Errorf("derive upstream path for %q: %w", name, err)
		}

		// A missing token is not fatal: public repos fetch without one, and a
		// private repo surfaces a clear 401 from GitHub rather than a confusing
		// proxy error.
		token, err := secretStore.Get(ctx, credential.GitTokenKey(tracked.Credential))
		if err != nil {
			slog.Warn("failed to read git token for repo, proceeding unauthenticated",
				"repo", name, "credential", tracked.Credential, "err", err)
		}

		return &gitproxy.Repo{
			Path:          path,
			Access:        tracked.EffectiveAccess(time.Now()),
			DefaultBranch: tracked.Branch,
			Token:         strings.TrimSpace(token),
		}, nil
	}
}
