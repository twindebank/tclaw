package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/repo"
	"tclaw/internal/user"
)

func TestRunRepoSweep(t *testing.T) {
	t.Run("withdraws an expired grant and keeps the clone", func(t *testing.T) {
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)
		tracked := lifecycleRepo(t, repoStore, userDir, repo.TrackedRepo{
			Name:             "ha-config",
			Access:           repo.AccessPullRequestsOnly,
			DropToReadOnlyAt: time.Now().Add(-time.Hour),
		})

		var notified []string
		runRepoSweep(context.Background(), repoSweepParams{
			Store:     repoStore,
			RemoteURL: func(string) string { return "" },
			Notify: func(_ context.Context, _ []string, text string) {
				notified = append(notified, text)
			},
		})

		after, err := repoStore.Get(context.Background(), "ha-config")
		require.NoError(t, err)
		require.Equal(t, repo.AccessReadOnly, after.Access)
		require.True(t, after.DropToReadOnlyAt.IsZero(), "the spent expiry should be cleared")
		require.Len(t, notified, 1, "an expiring grant must be reported, not silent")

		// The clone survives: only the capability was withdrawn.
		_, statErr := os.Stat(tracked.RepoDir)
		require.NoError(t, statErr)
	})

	t.Run("leaves a grant that has not expired", func(t *testing.T) {
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)
		lifecycleRepo(t, repoStore, userDir, repo.TrackedRepo{
			Name:             "ha-config",
			Access:           repo.AccessPullRequestsOnly,
			DropToReadOnlyAt: time.Now().Add(time.Hour),
		})

		runRepoSweep(context.Background(), repoSweepParams{
			Store:     repoStore,
			RemoteURL: func(string) string { return "" },
		})

		after, err := repoStore.Get(context.Background(), "ha-config")
		require.NoError(t, err)
		require.Equal(t, repo.AccessPullRequestsOnly, after.Access)
	})

	t.Run("drops an unused clone but keeps the entry", func(t *testing.T) {
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)
		tracked := lifecycleRepo(t, repoStore, userDir, repo.TrackedRepo{
			Name:                 "stale",
			DropCloneIfUnusedFor: time.Hour,
			LastUsedAt:           time.Now().Add(-48 * time.Hour),
		})

		runRepoSweep(context.Background(), repoSweepParams{
			Store:     repoStore,
			RemoteURL: func(string) string { return "" },
		})

		_, statErr := os.Stat(tracked.RepoDir)
		require.True(t, os.IsNotExist(statErr), "the clone should be gone")

		// The entry survives so the next sync recreates the clone.
		still, err := repoStore.Get(context.Background(), "stale")
		require.NoError(t, err)
		require.NotNil(t, still)
	})

	t.Run("keeps a clone that is still in use", func(t *testing.T) {
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)
		tracked := lifecycleRepo(t, repoStore, userDir, repo.TrackedRepo{
			Name:                 "fresh",
			DropCloneIfUnusedFor: 48 * time.Hour,
			LastUsedAt:           time.Now().Add(-time.Hour),
		})

		runRepoSweep(context.Background(), repoSweepParams{
			Store:     repoStore,
			RemoteURL: func(string) string { return "" },
		})

		_, statErr := os.Stat(tracked.RepoDir)
		require.NoError(t, statErr)
	})

	t.Run("refreshes a repo whose fetch interval has elapsed", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)

		require.NoError(t, provisionConfigRepos(context.Background(), reposProvisionParams{
			UserID:    "alice",
			UserDir:   userDir,
			Repos:     []user.Repo{{Name: "mirror", URL: remote, Branch: "main", FetchEvery: time.Minute}},
			Store:     repoStore,
			RemoteURL: directRemote(remote),
		}))

		// Backdate the sync so the interval has elapsed, and add a commit the
		// sweep should pick up.
		tracked, err := repoStore.Get(context.Background(), "mirror")
		require.NoError(t, err)
		tracked.LastSyncedAt = time.Now().Add(-time.Hour)
		require.NoError(t, repoStore.Put(context.Background(), *tracked))
		gitRun(t, remote, "commit", "--allow-empty", "-m", "later change")

		runRepoSweep(context.Background(), repoSweepParams{
			Store:     repoStore,
			RemoteURL: directRemote(remote),
		})

		after, err := repoStore.Get(context.Background(), "mirror")
		require.NoError(t, err)
		require.WithinDuration(t, time.Now(), after.LastSyncedAt, time.Minute)
		require.Equal(t, tracked.LastSeenCommit, after.LastSeenCommit,
			"a background fetch must not consume what the agent hasn't seen")
	})
}

// --- helpers ---

// lifecycleRepo stores a repo with its clone directory created.
func lifecycleRepo(t *testing.T, repoStore *repo.Store, userDir string, tracked repo.TrackedRepo) repo.TrackedRepo {
	t.Helper()
	tracked.RepoDir = filepath.Join(userDir, "repos", tracked.Name)
	require.NoError(t, os.MkdirAll(tracked.RepoDir, 0o755))
	require.NoError(t, repoStore.Put(context.Background(), tracked))
	return tracked
}
