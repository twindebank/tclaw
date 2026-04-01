package notification_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/libraries/store"
	"tclaw/internal/notification"
)

func TestStore_Add(t *testing.T) {
	t.Run("persists and is listed", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()

		sub := testSubscription("pkg", "type1", "channel1", notification.ScopePersistent)
		require.NoError(t, s.Add(ctx, sub))

		subs, err := s.List(ctx)
		require.NoError(t, err)
		require.Len(t, subs, 1)
		require.Equal(t, sub.ID, subs[0].ID)
		require.Equal(t, "pkg", subs[0].PackageName)
		require.Equal(t, "type1", subs[0].TypeName)
	})

	t.Run("rejects duplicate ID", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()

		sub := testSubscription("pkg", "type1", "ch", notification.ScopePersistent)
		require.NoError(t, s.Add(ctx, sub))

		err := s.Add(ctx, sub)
		require.Error(t, err)
		require.Contains(t, err.Error(), "already exists")
	})
}

func TestStore_Get(t *testing.T) {
	t.Run("returns existing subscription", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()

		sub := testSubscription("pkg", "type1", "ch", notification.ScopePersistent)
		require.NoError(t, s.Add(ctx, sub))

		got, err := s.Get(ctx, sub.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, sub.ID, got.ID)
	})

	t.Run("returns nil for missing ID", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()

		got, err := s.Get(ctx, "notif_nonexistent")
		require.NoError(t, err)
		require.Nil(t, got)
	})
}

func TestStore_Remove(t *testing.T) {
	t.Run("deletes the subscription", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()

		sub1 := testSubscription("pkg", "t1", "ch", notification.ScopePersistent)
		sub2 := testSubscription("pkg", "t2", "ch", notification.ScopePersistent)
		require.NoError(t, s.Add(ctx, sub1))
		require.NoError(t, s.Add(ctx, sub2))

		require.NoError(t, s.Remove(ctx, sub1.ID))

		subs, err := s.List(ctx)
		require.NoError(t, err)
		require.Len(t, subs, 1)
		require.Equal(t, sub2.ID, subs[0].ID)
	})

	t.Run("errors on missing ID", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()

		err := s.Remove(ctx, "notif_nonexistent")
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
	})
}

func TestStore_RemoveByCredentialSet(t *testing.T) {
	t.Run("removes matching and returns them", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()

		sub1 := testSubscription("google", "new_email", "ch1", notification.ScopeCredential)
		sub1.CredentialSetID = "google/work"
		sub2 := testSubscription("google", "new_email", "ch2", notification.ScopeCredential)
		sub2.CredentialSetID = "google/personal"
		sub3 := testSubscription("tfl", "disruption", "ch1", notification.ScopePersistent)

		require.NoError(t, s.Add(ctx, sub1))
		require.NoError(t, s.Add(ctx, sub2))
		require.NoError(t, s.Add(ctx, sub3))

		removed, err := s.RemoveByCredentialSet(ctx, "google/work")
		require.NoError(t, err)
		require.Len(t, removed, 1)
		require.Equal(t, sub1.ID, removed[0].ID)

		subs, err := s.List(ctx)
		require.NoError(t, err)
		require.Len(t, subs, 2)
	})

	t.Run("returns nil when none match", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()

		removed, err := s.RemoveByCredentialSet(ctx, "nonexistent")
		require.NoError(t, err)
		require.Nil(t, removed)
	})
}

func TestStore_List(t *testing.T) {
	t.Run("returns nil for empty store", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()

		subs, err := s.List(ctx)
		require.NoError(t, err)
		require.Nil(t, subs)
	})
}

// --- helpers ---

func newTestStore(t *testing.T) *notification.Store {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return notification.NewStore(s)
}

func testSubscription(pkg, typeName, channelName string, scope notification.Scope) notification.Subscription {
	return notification.Subscription{
		ID:          notification.GenerateID(),
		Scope:       scope,
		ChannelName: channelName,
		PackageName: pkg,
		TypeName:    typeName,
		Label:       pkg + "/" + typeName,
		CreatedAt:   time.Now(),
	}
}
