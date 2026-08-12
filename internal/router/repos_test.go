package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/libraries/store"
	"tclaw/internal/repo"
	"tclaw/internal/user"
)

func TestProvisionConfigRepos(t *testing.T) {
	t.Run("clones a declared repo and registers it as managed", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)

		require.NoError(t, provisionConfigRepos(context.Background(), reposProvisionParams{
			UserID:  "alice",
			UserDir: userDir,
			Repos: []user.Repo{{
				Name:        "ha-config",
				URL:         remote,
				Branch:      "main",
				Description: "Home Assistant config mirror",
				Channels:    []string{"homeassistant"},
			}},
			Store:   repoStore,
			Secrets: &memorySecretStore{data: map[string]string{}},
		}))

		repoDir := filepath.Join(userDir, "repos", "ha-config")
		_, err := os.Stat(filepath.Join(repoDir, "index.md"))
		require.NoError(t, err, "working tree should be checked out")

		tracked, err := repoStore.Get(context.Background(), "ha-config")
		require.NoError(t, err)
		require.NotNil(t, tracked)
		require.True(t, tracked.Managed)
		require.Equal(t, repoDir, tracked.RepoDir)
		require.Equal(t, []string{"homeassistant"}, tracked.Channels)
		require.Equal(t, "Home Assistant config mirror", tracked.Description)
	})

	t.Run("preserves the sync cursor across boots", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)
		declared := []user.Repo{{Name: "ha-config", URL: remote, Branch: "main"}}
		params := reposProvisionParams{
			UserID:  "alice",
			UserDir: userDir,
			Repos:   declared,
			Store:   repoStore,
			Secrets: &memorySecretStore{data: map[string]string{}},
		}

		require.NoError(t, provisionConfigRepos(context.Background(), params))

		// Stand in for a completed repo_sync.
		tracked, err := repoStore.Get(context.Background(), "ha-config")
		require.NoError(t, err)
		tracked.LastSeenCommit = "deadbeef"
		require.NoError(t, repoStore.Put(context.Background(), *tracked))

		require.NoError(t, provisionConfigRepos(context.Background(), params))

		after, err := repoStore.Get(context.Background(), "ha-config")
		require.NoError(t, err)
		require.Equal(t, "deadbeef", after.LastSeenCommit,
			"re-provisioning must not replay history the agent has already seen")
	})

	t.Run("drops a managed repo removed from config", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)
		secrets := &memorySecretStore{data: map[string]string{}}

		require.NoError(t, provisionConfigRepos(context.Background(), reposProvisionParams{
			UserID:  "alice",
			UserDir: userDir,
			Repos:   []user.Repo{{Name: "ha-config", URL: remote, Branch: "main"}},
			Store:   repoStore,
			Secrets: secrets,
		}))

		require.NoError(t, provisionConfigRepos(context.Background(), reposProvisionParams{
			UserID:  "alice",
			UserDir: userDir,
			Repos:   nil,
			Store:   repoStore,
			Secrets: secrets,
		}))

		gone, err := repoStore.Get(context.Background(), "ha-config")
		require.NoError(t, err)
		require.Nil(t, gone)

		_, statErr := os.Stat(filepath.Join(userDir, "repos", "ha-config"))
		require.True(t, os.IsNotExist(statErr), "clone directory should be cleaned up")
	})

	t.Run("leaves agent-added repos alone", func(t *testing.T) {
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)
		adhoc := repo.TrackedRepo{
			Name:    "scratch",
			URL:     "https://github.com/user/scratch",
			Branch:  "main",
			RepoDir: filepath.Join(userDir, "repos", "scratch"),
		}
		require.NoError(t, repoStore.Put(context.Background(), adhoc))

		require.NoError(t, provisionConfigRepos(context.Background(), reposProvisionParams{
			UserID:  "alice",
			UserDir: userDir,
			Repos:   nil,
			Store:   repoStore,
			Secrets: &memorySecretStore{data: map[string]string{}},
		}))

		still, err := repoStore.Get(context.Background(), "scratch")
		require.NoError(t, err)
		require.NotNil(t, still)
	})

	t.Run("one unreachable repo does not block the others", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)

		require.NoError(t, provisionConfigRepos(context.Background(), reposProvisionParams{
			UserID:  "alice",
			UserDir: userDir,
			Repos: []user.Repo{
				{Name: "broken", URL: filepath.Join(t.TempDir(), "does-not-exist"), Branch: "main"},
				{Name: "ha-config", URL: remote, Branch: "main"},
			},
			Store:   repoStore,
			Secrets: &memorySecretStore{data: map[string]string{}},
		}))

		_, err := os.Stat(filepath.Join(userDir, "repos", "ha-config", "index.md"))
		require.NoError(t, err, "the reachable repo should still be cloned")
	})
}

func TestResolveRepoMounts(t *testing.T) {
	t.Run("splits repos by channel scope", func(t *testing.T) {
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)
		putRepo(t, repoStore, "ha-config", userDir, []string{"homeassistant"})
		putRepo(t, repoStore, "notes", userDir, []string{"assistant"})
		putRepo(t, repoStore, "shared", userDir, nil)

		mounts := resolveRepoMounts(context.Background(), repoStore, "homeassistant")

		require.ElementsMatch(t, []string{
			filepath.Join(userDir, "repos", "ha-config"),
			filepath.Join(userDir, "repos", "shared"),
		}, mounts.Visible)
		require.Equal(t, []string{filepath.Join(userDir, "repos", "notes")}, mounts.Masked)
	})

	t.Run("skips repos whose clone is missing on disk", func(t *testing.T) {
		// A store entry can outlive its clone after a volume wipe; binding a
		// path that isn't there would fail the whole turn in bwrap.
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)
		putRepo(t, repoStore, "ha-config", userDir, []string{"homeassistant"})
		putRepo(t, repoStore, "shared", userDir, nil)
		require.NoError(t, os.RemoveAll(filepath.Join(userDir, "repos", "ha-config")))

		mounts := resolveRepoMounts(context.Background(), repoStore, "homeassistant")

		require.Equal(t, []string{filepath.Join(userDir, "repos", "shared")}, mounts.Visible)
		require.Empty(t, mounts.Masked)
	})

	t.Run("masks every scoped repo when the channel is unknown", func(t *testing.T) {
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)
		putRepo(t, repoStore, "ha-config", userDir, []string{"homeassistant"})
		putRepo(t, repoStore, "shared", userDir, nil)

		mounts := resolveRepoMounts(context.Background(), repoStore, "")

		require.Equal(t, []string{filepath.Join(userDir, "repos", "shared")}, mounts.Visible)
		require.Equal(t, []string{filepath.Join(userDir, "repos", "ha-config")}, mounts.Masked)
	})
}

// --- helpers ---

func newRepoStore(t *testing.T, userDir string) *repo.Store {
	t.Helper()
	s, err := store.NewFS(filepath.Join(userDir, "state"))
	require.NoError(t, err)
	return repo.NewStore(s)
}

// putRepo registers a tracked repo and creates its clone directory, which is
// what resolveRepoMounts keys mounting off.
func putRepo(t *testing.T, repoStore *repo.Store, name, userDir string, channels []string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(userDir, "repos", name), 0o755))
	require.NoError(t, repoStore.Put(context.Background(), repo.TrackedRepo{
		Name:     name,
		URL:      "https://github.com/user/" + name,
		Branch:   "main",
		RepoDir:  filepath.Join(userDir, "repos", name),
		Channels: channels,
	}))
}
