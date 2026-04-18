package channeltools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/config"
	"tclaw/internal/dev"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/reconciler"
	"tclaw/internal/tool/channeltools"
)

// TestChannelDelete_CleansUpBoundDevSessions exercises the full channel_delete
// tool path end to end: seed a channel and dev sessions, call channel_delete
// through the MCP handler, then assert both the session records and on-disk
// worktree directories are gone.
func TestChannelDelete_CleansUpBoundDevSessions(t *testing.T) {
	t.Run("deletes all sessions bound to the channel and leaves others alone", func(t *testing.T) {
		h, devStore := setupChannelDeleteWithDevStore(t)

		// Seed: two sessions bound to "scratch" (the channel we'll delete),
		// one bound to a different channel, one unbound (no CreatedByChannel).
		scratchWorktree1 := writeWorktreeDir(t, h.userDir, "feature-a")
		scratchWorktree2 := writeWorktreeDir(t, h.userDir, "feature-b")
		otherWorktree := writeWorktreeDir(t, h.userDir, "feature-c")
		unboundWorktree := writeWorktreeDir(t, h.userDir, "feature-d")

		ctx := context.Background()
		require.NoError(t, devStore.PutSession(ctx, dev.Session{
			Branch:           "feature-a",
			WorktreeDir:      scratchWorktree1,
			RepoDir:          filepath.Join(h.userDir, "repo"),
			Status:           dev.SessionActive,
			CreatedAt:        time.Now(),
			CreatedByChannel: "scratch",
		}))
		require.NoError(t, devStore.PutSession(ctx, dev.Session{
			Branch:           "feature-b",
			WorktreeDir:      scratchWorktree2,
			RepoDir:          filepath.Join(h.userDir, "repo"),
			Status:           dev.SessionActive,
			CreatedAt:        time.Now(),
			CreatedByChannel: "scratch",
		}))
		require.NoError(t, devStore.PutSession(ctx, dev.Session{
			Branch:           "feature-c",
			WorktreeDir:      otherWorktree,
			RepoDir:          filepath.Join(h.userDir, "repo"),
			Status:           dev.SessionActive,
			CreatedAt:        time.Now(),
			CreatedByChannel: "assistant",
		}))
		require.NoError(t, devStore.PutSession(ctx, dev.Session{
			Branch:      "feature-d",
			WorktreeDir: unboundWorktree,
			RepoDir:     filepath.Join(h.userDir, "repo"),
			Status:      dev.SessionActive,
			CreatedAt:   time.Now(),
		}))

		// Seed the channel to be deleted.
		require.NoError(t, h.configWriter.AddChannel(testUserID, config.Channel{
			Name:      "scratch",
			Type:      channel.TypeSocket,
			Ephemeral: true,
		}))
		reloadRegistryForDeleteTest(t, h)

		// Act.
		result := callTool(t, h.handler, "channel_delete", map[string]any{"name": "scratch"})

		var resp map[string]any
		require.NoError(t, json.Unmarshal(result, &resp))
		require.EqualValues(t, 2, resp["dev_sessions_removed"], "response must report the number of sessions torn down")
		require.Contains(t, resp["message"], "2 dev session", "response message should mention how many sessions were removed")

		// Assert: bound sessions removed, others kept.
		sessions, err := devStore.ListSessions(ctx)
		require.NoError(t, err)
		require.NotContains(t, sessions, "feature-a", "scratch-bound session a must be deleted")
		require.NotContains(t, sessions, "feature-b", "scratch-bound session b must be deleted")
		require.Contains(t, sessions, "feature-c", "session bound to another channel must survive")
		require.Contains(t, sessions, "feature-d", "unbound session must survive")

		// Assert: worktree dirs for removed sessions are gone.
		assertMissing(t, scratchWorktree1)
		assertMissing(t, scratchWorktree2)
		assertExists(t, otherWorktree)
		assertExists(t, unboundWorktree)
	})

	t.Run("reports zero when no sessions are bound to the channel", func(t *testing.T) {
		h, devStore := setupChannelDeleteWithDevStore(t)

		// A session bound to a different channel should be untouched.
		otherWorktree := writeWorktreeDir(t, h.userDir, "keep-me")
		require.NoError(t, devStore.PutSession(context.Background(), dev.Session{
			Branch:           "keep-me",
			WorktreeDir:      otherWorktree,
			RepoDir:          filepath.Join(h.userDir, "repo"),
			Status:           dev.SessionActive,
			CreatedAt:        time.Now(),
			CreatedByChannel: "assistant",
		}))

		require.NoError(t, h.configWriter.AddChannel(testUserID, config.Channel{
			Name:      "empty",
			Type:      channel.TypeSocket,
			Ephemeral: true,
		}))
		reloadRegistryForDeleteTest(t, h)

		result := callTool(t, h.handler, "channel_delete", map[string]any{"name": "empty"})

		var resp map[string]any
		require.NoError(t, json.Unmarshal(result, &resp))
		require.EqualValues(t, 0, resp["dev_sessions_removed"])
		require.NotContains(t, resp["message"], "dev session", "plain message when nothing to clean up")

		sessions, err := devStore.ListSessions(context.Background())
		require.NoError(t, err)
		require.Contains(t, sessions, "keep-me")
		assertExists(t, otherWorktree)
	})

	t.Run("works when DevStore is nil", func(t *testing.T) {
		// Regression: an environment without dev tools configured must not
		// panic on channel_delete.
		h := setupChannelDeleteNoDevStore(t)

		require.NoError(t, h.configWriter.AddChannel(testUserID, config.Channel{
			Name:      "no-dev",
			Type:      channel.TypeSocket,
			Ephemeral: true,
		}))
		reloadRegistryForDeleteTest(t, h)

		result := callTool(t, h.handler, "channel_delete", map[string]any{"name": "no-dev"})

		var resp map[string]any
		require.NoError(t, json.Unmarshal(result, &resp))
		require.EqualValues(t, 0, resp["dev_sessions_removed"])
	})
}

// --- helpers ---

type channelDeleteHarness struct {
	handler      *mcp.Handler
	configWriter *config.Writer
	registry     *channel.Registry
	runtimeState *channel.RuntimeStateStore
	userDir      string
}

func setupChannelDeleteWithDevStore(t *testing.T) (*channelDeleteHarness, *dev.Store) {
	t.Helper()
	h := buildDeleteHarness(t)
	devStore := dev.NewStore(mustFSStore(t, filepath.Join(h.userDir, "dev-state")))

	channeltools.RegisterTools(h.handler, channeltools.Deps{
		Registry:     h.registry,
		ConfigWriter: h.configWriter,
		RuntimeState: h.runtimeState,
		UserID:       testUserID,
		Env:          config.EnvLocal,
		SecretStore:  newMemorySecretStore(),
		DevStore:     devStore,
		ReconcileParams: reconciler.ReconcileParams{
			RuntimeState: h.runtimeState,
		},
	})
	return h, devStore
}

func setupChannelDeleteNoDevStore(t *testing.T) *channelDeleteHarness {
	t.Helper()
	h := buildDeleteHarness(t)

	channeltools.RegisterTools(h.handler, channeltools.Deps{
		Registry:     h.registry,
		ConfigWriter: h.configWriter,
		RuntimeState: h.runtimeState,
		UserID:       testUserID,
		Env:          config.EnvLocal,
		SecretStore:  newMemorySecretStore(),
		ReconcileParams: reconciler.ReconcileParams{
			RuntimeState: h.runtimeState,
		},
	})
	return h
}

func buildDeleteHarness(t *testing.T) *channelDeleteHarness {
	t.Helper()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "tclaw.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("local:\n  users:\n    - id: testuser\n      channels: []\n"), 0o644))

	stateFS := mustFSStore(t, filepath.Join(tmpDir, "state"))
	runtimeState := channel.NewRuntimeStateStore(stateFS)

	return &channelDeleteHarness{
		handler:      mcp.NewHandler(),
		configWriter: config.NewWriter(configPath, config.EnvLocal),
		registry:     channel.NewRegistry(nil),
		runtimeState: runtimeState,
		userDir:      tmpDir,
	}
}

func reloadRegistryForDeleteTest(t *testing.T, h *channelDeleteHarness) {
	t.Helper()
	channels, err := h.configWriter.ReadChannels(testUserID)
	require.NoError(t, err)

	var entries []channel.RegistryEntry
	for _, ch := range channels {
		entries = append(entries, channel.RegistryEntry{
			Info: channel.Info{
				ID:          channel.ChannelID(ch.Name),
				Type:        ch.Type,
				Name:        ch.Name,
				Description: ch.Description,
			},
		})
	}
	h.registry.Reload(entries)
}

func mustFSStore(t *testing.T, dir string) store.Store {
	t.Helper()
	s, err := store.NewFS(dir)
	require.NoError(t, err)
	return s
}

// writeWorktreeDir creates a fake worktree directory with a marker file and
// returns its absolute path. Tests assert the directory is removed after
// cleanup.
func writeWorktreeDir(t *testing.T, userDir, branch string) string {
	t.Helper()
	dir := filepath.Join(userDir, "worktrees", branch)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o644))
	return dir
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err), "expected %s to be removed, got err=%v", path, err)
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	require.NoError(t, err, "expected %s to still exist", path)
}
