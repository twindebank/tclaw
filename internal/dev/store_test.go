package dev_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/dev"
	"tclaw/internal/libraries/store"
)

func TestStoreDeleteSessionsByChannel(t *testing.T) {
	t.Run("removes every session bound to the channel", func(t *testing.T) {
		s, ctx := newTestStore(t)

		// Three sessions share the same channel — the data model must support
		// one channel owning multiple concurrent dev sessions.
		seedSession(t, s, ctx, "feature-a", "scratch")
		seedSession(t, s, ctx, "feature-b", "scratch")
		seedSession(t, s, ctx, "feature-c", "scratch")
		// Plus one bound to a different channel and one unbound.
		seedSession(t, s, ctx, "other", "assistant")
		seedSession(t, s, ctx, "solo", "")

		removed, err := s.DeleteSessionsByChannel(ctx, "scratch")
		require.NoError(t, err)
		require.Len(t, removed, 3, "all three scratch-bound sessions should be removed")

		branches := map[string]bool{}
		for _, sess := range removed {
			branches[sess.Branch] = true
			require.Equal(t, "scratch", sess.CreatedByChannel)
		}
		require.True(t, branches["feature-a"])
		require.True(t, branches["feature-b"])
		require.True(t, branches["feature-c"])

		remaining, err := s.ListSessions(ctx)
		require.NoError(t, err)
		require.Len(t, remaining, 2)
		require.Contains(t, remaining, "other")
		require.Contains(t, remaining, "solo")
	})

	t.Run("returns nil when no sessions match", func(t *testing.T) {
		s, ctx := newTestStore(t)
		seedSession(t, s, ctx, "feature", "other-channel")

		removed, err := s.DeleteSessionsByChannel(ctx, "missing")
		require.NoError(t, err)
		require.Empty(t, removed)

		// Store must be unchanged.
		remaining, err := s.ListSessions(ctx)
		require.NoError(t, err)
		require.Len(t, remaining, 1)
	})

	t.Run("returns nil for empty channel name (never matches unbound sessions)", func(t *testing.T) {
		s, ctx := newTestStore(t)
		// An unbound session must not get caught by a lookup for "".
		seedSession(t, s, ctx, "solo", "")

		removed, err := s.DeleteSessionsByChannel(ctx, "")
		require.NoError(t, err)
		require.Empty(t, removed)

		remaining, err := s.ListSessions(ctx)
		require.NoError(t, err)
		require.Contains(t, remaining, "solo", "unbound sessions must survive a '' lookup")
	})
}

// --- helpers ---

func newTestStore(t *testing.T) (*dev.Store, context.Context) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return dev.NewStore(s), context.Background()
}

func seedSession(t *testing.T, s *dev.Store, ctx context.Context, branch, channel string) {
	t.Helper()
	require.NoError(t, s.PutSession(ctx, dev.Session{
		Branch:           branch,
		WorktreeDir:      "/tmp/" + branch,
		RepoDir:          "/tmp/repo",
		Status:           dev.SessionActive,
		CreatedAt:        time.Now(),
		CreatedByChannel: channel,
	}))
}
