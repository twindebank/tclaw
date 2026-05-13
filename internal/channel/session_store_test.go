package channel_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
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

	t.Run("idempotent set bumps last_used_at", func(t *testing.T) {
		s := setupSessionStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetCurrent(ctx, "admin", "session-1"))
		firstRecords, err := s.List(ctx, "admin")
		require.NoError(t, err)
		require.Len(t, firstRecords, 1)
		firstLastUsed := firstRecords[0].LastUsedAt
		require.False(t, firstLastUsed.IsZero())

		time.Sleep(2 * time.Millisecond)
		require.NoError(t, s.SetCurrent(ctx, "admin", "session-1"))

		records, err := s.List(ctx, "admin")
		require.NoError(t, err)
		require.Len(t, records, 1)
		require.True(t, records[0].LastUsedAt.After(firstLastUsed),
			"last_used_at should advance on repeated SetCurrent for the same session")
	})

	t.Run("CurrentWithin returns session when fresh", func(t *testing.T) {
		s := setupSessionStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetCurrent(ctx, "admin", "session-1"))

		sid, err := s.CurrentWithin(ctx, "admin", time.Hour)
		require.NoError(t, err)
		require.Equal(t, "session-1", sid)
	})

	t.Run("CurrentWithin returns empty when stale", func(t *testing.T) {
		s := setupSessionStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetCurrent(ctx, "admin", "session-1"))

		// Force the record to look old by rewriting its LastUsedAt in the past.
		records, err := s.List(ctx, "admin")
		require.NoError(t, err)
		require.Len(t, records, 1)
		records[0].LastUsedAt = time.Now().Add(-time.Hour)
		// Round-trip via saveRecords helper — use SetCurrent for a brand-new
		// session-2 first, then re-write to exercise the path. Easier: just
		// inspect the duration check by passing a maxAge of 1ns.
		_ = records

		// Anything older than 1ns is stale.
		sid, err := s.CurrentWithin(ctx, "admin", time.Nanosecond)
		require.NoError(t, err)
		require.Equal(t, "", sid)
	})

	t.Run("CurrentWithin treats zero maxAge as no expiry", func(t *testing.T) {
		s := setupSessionStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetCurrent(ctx, "admin", "session-1"))

		sid, err := s.CurrentWithin(ctx, "admin", 0)
		require.NoError(t, err)
		require.Equal(t, "session-1", sid)
	})

	t.Run("CurrentWithin returns empty for cleared session", func(t *testing.T) {
		s := setupSessionStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetCurrent(ctx, "admin", "session-1"))
		require.NoError(t, s.SetCurrent(ctx, "admin", ""))

		sid, err := s.CurrentWithin(ctx, "admin", time.Hour)
		require.NoError(t, err)
		require.Equal(t, "", sid)
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
		got := channel.SessionKey("/tmp/tclaw/alice/admin.sock")
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
