package channel_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/libraries/store"
)

func TestQueueStore_SaveLoad(t *testing.T) {
	s := newQueueStore(t)
	ctx := context.Background()

	messages := []channel.QueuedMessage{
		{
			ChannelID: "ch1",
			Text:      "hello from channel 1",
			QueuedAt:  time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC),
		},
		{
			ChannelID: "ch2",
			Text:      "hello from channel 2",
			QueuedAt:  time.Date(2026, 3, 25, 10, 1, 0, 0, time.UTC),
		},
	}

	require.NoError(t, s.Save(ctx, messages))

	state, err := s.Load(ctx)
	require.NoError(t, err)
	require.Len(t, state.Messages, 2)
	require.Equal(t, channel.ChannelID("ch1"), state.Messages[0].ChannelID)
	require.Equal(t, "hello from channel 1", state.Messages[0].Text)
	require.Equal(t, channel.ChannelID("ch2"), state.Messages[1].ChannelID)
	require.Equal(t, "hello from channel 2", state.Messages[1].Text)
}

func TestQueueStore_EmptyLoad(t *testing.T) {
	s := newQueueStore(t)
	ctx := context.Background()

	state, err := s.Load(ctx)
	require.NoError(t, err)
	require.Empty(t, state.Messages)
	require.Empty(t, state.InterruptedChannel)
}

func TestQueueStore_Overwrite(t *testing.T) {
	s := newQueueStore(t)
	ctx := context.Background()

	first := []channel.QueuedMessage{
		{ChannelID: "ch1", Text: "first", QueuedAt: time.Now()},
		{ChannelID: "ch2", Text: "second", QueuedAt: time.Now()},
	}
	require.NoError(t, s.Save(ctx, first))

	second := []channel.QueuedMessage{
		{ChannelID: "ch3", Text: "replaced", QueuedAt: time.Now()},
	}
	require.NoError(t, s.Save(ctx, second))

	state, err := s.Load(ctx)
	require.NoError(t, err)
	require.Len(t, state.Messages, 1)
	require.Equal(t, "replaced", state.Messages[0].Text)
}

func TestQueueStore_WithSourceInfo(t *testing.T) {
	s := newQueueStore(t)
	ctx := context.Background()

	messages := []channel.QueuedMessage{
		{
			ChannelID: "ch1",
			Text:      "scheduled message",
			SourceInfo: &channel.MessageSourceInfo{
				Source:       channel.SourceSchedule,
				ScheduleName: "daily-check",
			},
			QueuedAt: time.Now(),
		},
		{
			ChannelID: "ch2",
			Text:      "cross-channel message",
			SourceInfo: &channel.MessageSourceInfo{
				Source:      channel.SourceChannel,
				FromChannel: "admin",
			},
			QueuedAt: time.Now(),
		},
	}
	require.NoError(t, s.Save(ctx, messages))

	state, err := s.Load(ctx)
	require.NoError(t, err)
	require.Len(t, state.Messages, 2)
	require.Equal(t, channel.SourceSchedule, state.Messages[0].SourceInfo.Source)
	require.Equal(t, "daily-check", state.Messages[0].SourceInfo.ScheduleName)
	require.Equal(t, channel.SourceChannel, state.Messages[1].SourceInfo.Source)
	require.Equal(t, "admin", state.Messages[1].SourceInfo.FromChannel)
}

func TestQueueStore_InterruptedChannel(t *testing.T) {
	t.Run("set and load", func(t *testing.T) {
		s := newQueueStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetInterrupted(ctx, "ch1"))

		state, err := s.Load(ctx)
		require.NoError(t, err)
		require.Equal(t, channel.ChannelID("ch1"), state.InterruptedChannel)
	})

	t.Run("clear", func(t *testing.T) {
		s := newQueueStore(t)
		ctx := context.Background()

		require.NoError(t, s.SetInterrupted(ctx, "ch1"))
		require.NoError(t, s.ClearInterrupted(ctx))

		state, err := s.Load(ctx)
		require.NoError(t, err)
		require.Empty(t, state.InterruptedChannel)
	})

	t.Run("preserved across save", func(t *testing.T) {
		s := newQueueStore(t)
		ctx := context.Background()

		// Set interrupted, then save messages — interrupted marker should persist.
		require.NoError(t, s.SetInterrupted(ctx, "ch1"))
		require.NoError(t, s.Save(ctx, []channel.QueuedMessage{
			{ChannelID: "ch2", Text: "queued", QueuedAt: time.Now()},
		}))

		state, err := s.Load(ctx)
		require.NoError(t, err)
		require.Equal(t, channel.ChannelID("ch1"), state.InterruptedChannel)
		require.Len(t, state.Messages, 1)
	})
}

// --- helpers ---

func newQueueStore(t *testing.T) *channel.QueueStore {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return channel.NewQueueStore(s)
}
