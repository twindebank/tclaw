package notification_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/libraries/store"
	"tclaw/notification"
)

func TestPendingStore_Add(t *testing.T) {
	t.Run("persists and is listed", func(t *testing.T) {
		s := newTestPendingStore(t)
		ctx := context.Background()

		pn := notification.PendingNotification{
			ID:             notification.GeneratePendingID(),
			SubscriptionID: "notif_abc",
			ChannelName:    "phone",
			Text:           "New email from bob",
			QueuedAt:       time.Now(),
			Scope:          notification.ScopePersistent,
			Label:          "google/new_email",
		}
		require.NoError(t, s.Add(ctx, pn))

		items, err := s.List(ctx)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, pn.ID, items[0].ID)
		require.Equal(t, "New email from bob", items[0].Text)
	})
}

func TestPendingStore_Remove(t *testing.T) {
	t.Run("deletes by ID", func(t *testing.T) {
		s := newTestPendingStore(t)
		ctx := context.Background()

		pn1 := notification.PendingNotification{
			ID:          notification.GeneratePendingID(),
			ChannelName: "ch1",
			Text:        "first",
			QueuedAt:    time.Now(),
		}
		pn2 := notification.PendingNotification{
			ID:          notification.GeneratePendingID(),
			ChannelName: "ch2",
			Text:        "second",
			QueuedAt:    time.Now(),
		}
		require.NoError(t, s.Add(ctx, pn1))
		require.NoError(t, s.Add(ctx, pn2))

		require.NoError(t, s.Remove(ctx, pn1.ID))

		items, err := s.List(ctx)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, pn2.ID, items[0].ID)
	})
}

func TestPendingStore_List(t *testing.T) {
	t.Run("returns nil for empty store", func(t *testing.T) {
		s := newTestPendingStore(t)
		ctx := context.Background()

		items, err := s.List(ctx)
		require.NoError(t, err)
		require.Nil(t, items)
	})
}

// --- helpers ---

func newTestPendingStore(t *testing.T) *notification.PendingStore {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return notification.NewPendingStore(s)
}
