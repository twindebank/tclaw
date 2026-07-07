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

func TestStoreResolveSession(t *testing.T) {
	t.Run("auto-selects this channel's only session even when other channels have sessions", func(t *testing.T) {
		// Reproduces the incident: two dev channels each had a session. dev_end
		// from one channel must resolve unambiguously to that channel's session
		// and never touch the other channel's work.
		s, ctx := newTestStore(t)
		seedSession(t, s, ctx, "2026-07-07-add-googlegmailmodify-tool", "dev-gws-modify-tool")
		seedSession(t, s, ctx, "2026-07-07-hard-enforce-noreply", "dev-noreply-enforce")

		sess, err := s.ResolveSession(ctx, dev.ResolveParams{Channel: "dev-gws-modify-tool"})
		require.NoError(t, err)
		require.Equal(t, "2026-07-07-add-googlegmailmodify-tool", sess.Branch)
	})

	t.Run("refuses to resolve another channel's session by name", func(t *testing.T) {
		s, ctx := newTestStore(t)
		seedSession(t, s, ctx, "mine", "channel-a")
		seedSession(t, s, ctx, "theirs", "channel-b")

		_, err := s.ResolveSession(ctx, dev.ResolveParams{Session: "theirs", Channel: "channel-a"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "belongs to channel")
	})

	t.Run("resolves this channel's session by name", func(t *testing.T) {
		s, ctx := newTestStore(t)
		seedSession(t, s, ctx, "mine", "channel-a")
		seedSession(t, s, ctx, "theirs", "channel-b")

		sess, err := s.ResolveSession(ctx, dev.ResolveParams{Session: "mine", Channel: "channel-a"})
		require.NoError(t, err)
		require.Equal(t, "mine", sess.Branch)
	})

	t.Run("errors when this channel has multiple sessions and none named", func(t *testing.T) {
		s, ctx := newTestStore(t)
		seedSession(t, s, ctx, "one", "channel-a")
		seedSession(t, s, ctx, "two", "channel-a")

		_, err := s.ResolveSession(ctx, dev.ResolveParams{Channel: "channel-a"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "multiple active sessions in this channel")
	})

	t.Run("errors when this channel has no sessions but others do", func(t *testing.T) {
		s, ctx := newTestStore(t)
		seedSession(t, s, ctx, "theirs", "channel-b")

		_, err := s.ResolveSession(ctx, dev.ResolveParams{Channel: "channel-a"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "no active dev sessions for this channel")
	})

	t.Run("empty channel scope matches any session (stdio/tests)", func(t *testing.T) {
		s, ctx := newTestStore(t)
		seedSession(t, s, ctx, "solo", "some-channel")

		sess, err := s.ResolveSession(ctx, dev.ResolveParams{})
		require.NoError(t, err)
		require.Equal(t, "solo", sess.Branch)
	})

	t.Run("channel-less session is actionable from any channel", func(t *testing.T) {
		s, ctx := newTestStore(t)
		seedSession(t, s, ctx, "unbound", "")

		sess, err := s.ResolveSession(ctx, dev.ResolveParams{Session: "unbound", Channel: "channel-a"})
		require.NoError(t, err)
		require.Equal(t, "unbound", sess.Branch)
	})
}

func TestStoreListSessionsForChannel(t *testing.T) {
	t.Run("returns only this channel's sessions plus channel-less ones", func(t *testing.T) {
		s, ctx := newTestStore(t)
		seedSession(t, s, ctx, "mine", "channel-a")
		seedSession(t, s, ctx, "theirs", "channel-b")
		seedSession(t, s, ctx, "unbound", "")

		got, err := s.ListSessionsForChannel(ctx, "channel-a")
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.Contains(t, got, "mine")
		require.Contains(t, got, "unbound")
		require.NotContains(t, got, "theirs")
	})

	t.Run("empty channel returns all sessions", func(t *testing.T) {
		s, ctx := newTestStore(t)
		seedSession(t, s, ctx, "a", "channel-a")
		seedSession(t, s, ctx, "b", "channel-b")

		got, err := s.ListSessionsForChannel(ctx, "")
		require.NoError(t, err)
		require.Len(t, got, 2)
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
