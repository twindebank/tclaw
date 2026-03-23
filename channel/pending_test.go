package channel_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/libraries/store"
)

func TestPendingStore(t *testing.T) {
	t.Run("add and list", func(t *testing.T) {
		s := setupPendingStore(t)

		msg := channel.PendingMessage{
			ID:          "pending_1",
			FromChannel: "admin",
			ToChannel:   "assistant",
			Message:     "check emails",
			QueuedAt:    time.Now(),
			ExpiresAt:   time.Now().Add(30 * time.Minute),
		}

		err := s.Add(context.Background(), msg)
		require.NoError(t, err)

		messages, err := s.List(context.Background())
		require.NoError(t, err)
		require.Len(t, messages, 1)
		require.Equal(t, "pending_1", messages[0].ID)
		require.Equal(t, "check emails", messages[0].Message)
	})

	t.Run("add multiple", func(t *testing.T) {
		s := setupPendingStore(t)

		for i, id := range []string{"p1", "p2", "p3"} {
			err := s.Add(context.Background(), channel.PendingMessage{
				ID:          id,
				FromChannel: "admin",
				ToChannel:   "assistant",
				Message:     "msg " + id,
				QueuedAt:    time.Now(),
				ExpiresAt:   time.Now().Add(time.Duration(i+1) * time.Minute),
			})
			require.NoError(t, err)
		}

		messages, err := s.List(context.Background())
		require.NoError(t, err)
		require.Len(t, messages, 3)
	})

	t.Run("remove by ID", func(t *testing.T) {
		s := setupPendingStore(t)

		for _, id := range []string{"p1", "p2", "p3"} {
			err := s.Add(context.Background(), channel.PendingMessage{
				ID:      id,
				Message: id,
			})
			require.NoError(t, err)
		}

		err := s.Remove(context.Background(), "p2")
		require.NoError(t, err)

		messages, err := s.List(context.Background())
		require.NoError(t, err)
		require.Len(t, messages, 2)
		require.Equal(t, "p1", messages[0].ID)
		require.Equal(t, "p3", messages[1].ID)
	})

	t.Run("remove nonexistent is no-op", func(t *testing.T) {
		s := setupPendingStore(t)

		err := s.Add(context.Background(), channel.PendingMessage{ID: "p1"})
		require.NoError(t, err)

		err = s.Remove(context.Background(), "nonexistent")
		require.NoError(t, err)

		messages, err := s.List(context.Background())
		require.NoError(t, err)
		require.Len(t, messages, 1)
	})

	t.Run("list empty store", func(t *testing.T) {
		s := setupPendingStore(t)

		messages, err := s.List(context.Background())
		require.NoError(t, err)
		require.Nil(t, messages)
	})

	t.Run("persists across instances", func(t *testing.T) {
		// Simulates restart — create a new PendingStore from the same underlying store.
		fs, err := store.NewFS(t.TempDir())
		require.NoError(t, err)

		s1 := channel.NewPendingStore(fs)
		err = s1.Add(context.Background(), channel.PendingMessage{
			ID:      "survive",
			Message: "I persist",
		})
		require.NoError(t, err)

		// Create a new PendingStore pointing at the same store.
		s2 := channel.NewPendingStore(fs)
		messages, err := s2.List(context.Background())
		require.NoError(t, err)
		require.Len(t, messages, 1)
		require.Equal(t, "survive", messages[0].ID)
		require.Equal(t, "I persist", messages[0].Message)
	})
}

func TestActivityTracker_BusyWithTimeout(t *testing.T) {
	t.Run("not busy when no activity", func(t *testing.T) {
		tracker := channel.NewActivityTracker()

		require.False(t, tracker.IsBusyWithTimeout("test", 5*time.Minute))
	})

	t.Run("busy while processing", func(t *testing.T) {
		tracker := channel.NewActivityTracker()
		tracker.TurnStarted("test")

		require.True(t, tracker.IsBusyWithTimeout("test", 0))
	})

	t.Run("short timeout expires quickly", func(t *testing.T) {
		tracker := channel.NewActivityTracker()
		tracker.MessageReceived("test")

		// With 0 timeout, only processing flag matters.
		require.False(t, tracker.IsBusyWithTimeout("test", 0))
	})

	t.Run("long timeout keeps busy longer", func(t *testing.T) {
		tracker := channel.NewActivityTracker()
		tracker.MessageReceived("test")

		// With a very long timeout, should still be busy.
		require.True(t, tracker.IsBusyWithTimeout("test", 1*time.Hour))
	})
}

// --- helpers ---

func setupPendingStore(t *testing.T) *channel.PendingStore {
	t.Helper()
	fs, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return channel.NewPendingStore(fs)
}
