package router

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
)

func TestCleanupStaleSessions(t *testing.T) {
	t.Run("deletes stale session files and keeps active ones", func(t *testing.T) {
		ctx := context.Background()
		sessionsDir := t.TempDir()
		homeDir := t.TempDir()

		sessionFS, err := store.NewFS(sessionsDir)
		require.NoError(t, err)
		sessionStore := channel.NewSessionStore(sessionFS)

		// Create two sessions: one active (current for a channel) and one stale.
		require.NoError(t, sessionStore.SetCurrent(ctx, "telegram_admin", "active-session"))
		require.NoError(t, sessionStore.SetCurrent(ctx, "telegram_admin", "stale-session"))
		require.NoError(t, sessionStore.SetCurrent(ctx, "telegram_admin", "active-session"))

		// Set up CLI project directory with session files.
		projectDir := filepath.Join(homeDir, ".claude", "projects", "test-project")
		require.NoError(t, os.MkdirAll(projectDir, 0o755))

		// Active session file (recent).
		writeSessionFile(t, projectDir, "active-session", time.Now())

		// Stale session file (old) with a data directory.
		writeSessionFile(t, projectDir, "stale-session", time.Now().Add(-10*24*time.Hour))
		staleDataDir := filepath.Join(projectDir, "stale-session")
		require.NoError(t, os.MkdirAll(filepath.Join(staleDataDir, "subagents"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(staleDataDir, "subagents", "agent.jsonl"), []byte("data"), 0o644))

		// Another old file that's NOT tracked in any session store — should still be cleaned.
		writeSessionFile(t, projectDir, "orphaned-session", time.Now().Add(-14*24*time.Hour))

		cleanupStaleSessions(ctx, sessionStore, sessionFS, sessionsDir, homeDir)

		// Active session file still exists.
		_, err = os.Stat(filepath.Join(projectDir, "active-session.jsonl"))
		require.NoError(t, err, "active session file should survive cleanup")

		// Stale session file and directory deleted.
		_, err = os.Stat(filepath.Join(projectDir, "stale-session.jsonl"))
		require.True(t, os.IsNotExist(err), "stale session file should be deleted")
		_, err = os.Stat(staleDataDir)
		require.True(t, os.IsNotExist(err), "stale session data dir should be deleted")

		// Orphaned session file deleted.
		_, err = os.Stat(filepath.Join(projectDir, "orphaned-session.jsonl"))
		require.True(t, os.IsNotExist(err), "orphaned session file should be deleted")
	})

	t.Run("prunes old session store records", func(t *testing.T) {
		ctx := context.Background()
		sessionsDir := t.TempDir()
		homeDir := t.TempDir()

		sessionFS, err := store.NewFS(sessionsDir)
		require.NoError(t, err)
		sessionStore := channel.NewSessionStore(sessionFS)

		// Create a channel with multiple session records.
		require.NoError(t, sessionStore.SetCurrent(ctx, "telegram_admin", "old-session"))
		require.NoError(t, sessionStore.SetCurrent(ctx, "telegram_admin", "current-session"))

		// Backdate the old session record by writing directly.
		records, err := sessionStore.List(ctx, "telegram_admin")
		require.NoError(t, err)
		require.Len(t, records, 2)
		records[0].StartedAt = time.Now().Add(-14 * 24 * time.Hour)
		backdated, err := json.Marshal(records)
		require.NoError(t, err)
		require.NoError(t, sessionFS.Set(ctx, "telegram_admin", backdated))

		// Create projects dir (even if empty, cleanup needs it).
		require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".claude", "projects"), 0o755))

		cleanupStaleSessions(ctx, sessionStore, sessionFS, sessionsDir, homeDir)

		// Only the current session record should remain.
		remaining, err := sessionStore.List(ctx, "telegram_admin")
		require.NoError(t, err)
		require.Len(t, remaining, 1)
		require.Equal(t, "current-session", remaining[0].SessionID)
	})

	t.Run("does nothing when no stale sessions exist", func(t *testing.T) {
		ctx := context.Background()
		sessionsDir := t.TempDir()
		homeDir := t.TempDir()

		sessionFS, err := store.NewFS(sessionsDir)
		require.NoError(t, err)
		sessionStore := channel.NewSessionStore(sessionFS)

		require.NoError(t, sessionStore.SetCurrent(ctx, "telegram_admin", "fresh-session"))

		projectDir := filepath.Join(homeDir, ".claude", "projects", "test-project")
		require.NoError(t, os.MkdirAll(projectDir, 0o755))
		writeSessionFile(t, projectDir, "fresh-session", time.Now())

		// Should complete without error and not delete anything.
		cleanupStaleSessions(ctx, sessionStore, sessionFS, sessionsDir, homeDir)

		_, err = os.Stat(filepath.Join(projectDir, "fresh-session.jsonl"))
		require.NoError(t, err, "fresh session should survive")
	})
}

// --- helpers ---

func writeSessionFile(t *testing.T, dir string, sessionID string, mtime time.Time) {
	t.Helper()
	path := filepath.Join(dir, sessionID+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"test": true}`), 0o644))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
}
