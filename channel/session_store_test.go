package channel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/libraries/store"
)

func TestSessionStore(t *testing.T) {
	t.Run("current returns empty for new channel", func(t *testing.T) {
		s := setupSessionStore(t)

		sid, err := s.Current(context.Background(), "admin")
		require.NoError(t, err)
		require.Equal(t, "", sid)
	})

	t.Run("set and get current", func(t *testing.T) {
		s := setupSessionStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetCurrent(ctx, "admin", "session-1"))

		sid, err := s.Current(ctx, "admin")
		require.NoError(t, err)
		require.Equal(t, "session-1", sid)
	})

	t.Run("new session replaces current", func(t *testing.T) {
		s := setupSessionStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetCurrent(ctx, "admin", "session-1"))
		require.NoError(t, s.SetCurrent(ctx, "admin", "session-2"))

		sid, err := s.Current(ctx, "admin")
		require.NoError(t, err)
		require.Equal(t, "session-2", sid)
	})

	t.Run("clear preserves history", func(t *testing.T) {
		s := setupSessionStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetCurrent(ctx, "admin", "session-1"))
		require.NoError(t, s.SetCurrent(ctx, "admin", "session-2"))

		// Clear (session reset).
		require.NoError(t, s.SetCurrent(ctx, "admin", ""))

		// Current should be empty.
		sid, err := s.Current(ctx, "admin")
		require.NoError(t, err)
		require.Equal(t, "", sid)

		// History should still have both sessions.
		records, err := s.List(ctx, "admin")
		require.NoError(t, err)
		require.Len(t, records, 2)
		require.Equal(t, "session-1", records[0].SessionID)
		require.Equal(t, "session-2", records[1].SessionID)
		require.True(t, records[1].Cleared)
	})

	t.Run("set after clear starts new session", func(t *testing.T) {
		s := setupSessionStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetCurrent(ctx, "admin", "session-1"))
		require.NoError(t, s.SetCurrent(ctx, "admin", ""))
		require.NoError(t, s.SetCurrent(ctx, "admin", "session-2"))

		sid, err := s.Current(ctx, "admin")
		require.NoError(t, err)
		require.Equal(t, "session-2", sid)

		records, err := s.List(ctx, "admin")
		require.NoError(t, err)
		require.Len(t, records, 2)
	})

	t.Run("idempotent set", func(t *testing.T) {
		s := setupSessionStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetCurrent(ctx, "admin", "session-1"))
		require.NoError(t, s.SetCurrent(ctx, "admin", "session-1"))

		records, err := s.List(ctx, "admin")
		require.NoError(t, err)
		require.Len(t, records, 1)
	})

	t.Run("independent channels", func(t *testing.T) {
		s := setupSessionStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetCurrent(ctx, "admin", "sess-a"))
		require.NoError(t, s.SetCurrent(ctx, "assistant", "sess-b"))

		sidA, err := s.Current(ctx, "admin")
		require.NoError(t, err)
		require.Equal(t, "sess-a", sidA)

		sidB, err := s.Current(ctx, "assistant")
		require.NoError(t, err)
		require.Equal(t, "sess-b", sidB)
	})

	t.Run("migrates legacy plain string format", func(t *testing.T) {
		fs, err := store.NewFS(t.TempDir())
		require.NoError(t, err)
		ctx := context.Background()

		// Write old-format data: plain session ID string.
		require.NoError(t, fs.Set(ctx, "admin", []byte("legacy-session-id")))

		s := channel.NewSessionStore(fs)

		// Current should read the legacy value.
		sid, err := s.Current(ctx, "admin")
		require.NoError(t, err)
		require.Equal(t, "legacy-session-id", sid)

		// List should show the migrated record.
		records, err := s.List(ctx, "admin")
		require.NoError(t, err)
		require.Len(t, records, 1)
		require.Equal(t, "legacy-session-id", records[0].SessionID)
	})
}

func TestSessionKey(t *testing.T) {
	t.Run("replaces slashes", func(t *testing.T) {
		got := channel.SessionKey("/tmp/tclaw/theo/admin.sock")
		require.Equal(t, "_tmp_tclaw_theo_admin.sock", got)
	})

	t.Run("simple name unchanged", func(t *testing.T) {
		got := channel.SessionKey("admin")
		require.Equal(t, "admin", got)
	})
}

// --- helpers ---

func setupSessionStore(t *testing.T) *channel.SessionStore {
	t.Helper()
	fs, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return channel.NewSessionStore(fs)
}
