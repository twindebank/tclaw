package router

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
	"tclaw/internal/queue"
)

func TestMergeAgentInputs(t *testing.T) {
	t.Run("requeues an in-flight non-user message when the iteration is cancelled", func(t *testing.T) {
		q := newMergeTestQueue(t)
		ctx, cancel := context.WithCancel(context.Background())
		static := make(chan channel.TaggedMessage)
		dynamic := make(chan channel.TaggedMessage)

		// No consumer reads out, so the merge goroutine pulls the message and
		// blocks on delivery — the exact restart-boundary race.
		_ = mergeAgentInputs(ctx, q, static, dynamic)

		static <- channel.TaggedMessage{
			ChannelID:  "ch1",
			Text:       "scheduled fire",
			SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceSchedule},
		}

		cancel()

		require.Eventually(t, func() bool { return q.Len() == 1 }, time.Second, 5*time.Millisecond,
			"the undelivered schedule message must be requeued, not dropped")
	})

	t.Run("delivers normally when a consumer is reading", func(t *testing.T) {
		q := newMergeTestQueue(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		static := make(chan channel.TaggedMessage)
		dynamic := make(chan channel.TaggedMessage)

		out := mergeAgentInputs(ctx, q, static, dynamic)

		want := channel.TaggedMessage{
			ChannelID:  "ch1",
			Text:       "live",
			SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceUser},
		}
		go func() { dynamic <- want }()

		select {
		case got := <-out:
			require.Equal(t, "live", got.Text)
		case <-time.After(time.Second):
			t.Fatal("message was not delivered to out")
		}
		require.Equal(t, 0, q.Len(), "delivered messages must not also be requeued")
	})
}

// --- helpers ---

func newMergeTestQueue(t *testing.T) *queue.Queue {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return queue.New(queue.QueueParams{
		Store:    s,
		Activity: channel.NewActivityTracker(),
		Channels: func() map[channel.ChannelID]channel.Channel { return nil },
	})
}
