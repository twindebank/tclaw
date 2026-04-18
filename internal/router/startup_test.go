package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
	"tclaw/internal/queue"
)

func TestDetermineStartupSignal(t *testing.T) {
	t.Run("empty queue with no interrupted marker blocks for a live message", func(t *testing.T) {
		q, channels := setupStartupQueue(t)

		decision := determineStartupSignal(context.Background(), q, channels)
		require.False(t, decision.StartNow, "empty queue with no marker should block for a live message")
		require.Nil(t, decision.FirstMessage)
	})

	t.Run("interrupted marker yields a synthetic resume message", func(t *testing.T) {
		q, channels := setupStartupQueue(t)
		ctx := context.Background()
		require.NoError(t, q.SetInterrupted(ctx, "ch1"))

		decision := determineStartupSignal(ctx, q, channels)
		require.True(t, decision.StartNow)
		require.NotNil(t, decision.FirstMessage)
		require.Equal(t, channel.ChannelID("ch1"), decision.FirstMessage.ChannelID)
		require.Equal(t, channel.SourceResume, decision.FirstMessage.SourceInfo.Source)
	})

	// This test reproduces the production bug observed on 2026-04-18 at
	// 10:00 BST: a scheduled admin turn was interrupted by a deploy restart,
	// leaving a persisted queued message behind. checkAutoResume returned
	// nil because no interrupted marker was set (the scheduler push happened
	// pre-turn-start, so SetInterrupted was never called). The router then
	// blocked on mergedMsgs for a live message, leaving the queued work
	// stranded for ~1m47s until an unrelated assistant-channel message
	// arrived and forced an agent start.
	t.Run("persisted non-resume work after restart starts the agent with no synthetic turn", func(t *testing.T) {
		s, err := store.NewFS(t.TempDir())
		require.NoError(t, err)
		ctx := context.Background()

		channels := map[channel.ChannelID]channel.Channel{
			"admin": &stubResumeChannel{id: "admin"},
		}
		activity := channel.NewActivityTracker()
		channelsFunc := func() map[channel.ChannelID]channel.Channel { return channels }

		// Persist a scheduled admin message on the pre-restart queue, then
		// drop it — simulating SIGTERM between Push and processing.
		pre := queue.New(queue.QueueParams{Store: s, Activity: activity, Channels: channelsFunc})
		require.NoError(t, pre.Push(ctx, channel.TaggedMessage{
			ChannelID:  "admin",
			Text:       "daily summary",
			SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceSchedule},
		}))

		// Fresh queue on "new process", reload from disk.
		post := queue.New(queue.QueueParams{Store: s, Activity: activity, Channels: channelsFunc})
		require.NoError(t, post.LoadPersisted(ctx))
		require.Equal(t, 1, post.Len(), "persisted message must round-trip through LoadPersisted")

		decision := determineStartupSignal(ctx, post, channels)
		require.True(t, decision.StartNow,
			"router must start immediately when queue has persisted work — "+
				"otherwise waitAndStart blocks on live inbound messages and the queued "+
				"work stays stranded until an unrelated message arrives")
		require.Nil(t, decision.FirstMessage,
			"persisted work needs no synthetic first turn — Queue.Next() drains the queue itself")
	})

	t.Run("resume marker wins over plain persisted queue work", func(t *testing.T) {
		s, err := store.NewFS(t.TempDir())
		require.NoError(t, err)
		ctx := context.Background()

		channels := map[channel.ChannelID]channel.Channel{
			"admin":     &stubResumeChannel{id: "admin"},
			"assistant": &stubResumeChannel{id: "assistant"},
		}
		activity := channel.NewActivityTracker()
		channelsFunc := func() map[channel.ChannelID]channel.Channel { return channels }

		pre := queue.New(queue.QueueParams{Store: s, Activity: activity, Channels: channelsFunc})
		require.NoError(t, pre.Push(ctx, channel.TaggedMessage{
			ChannelID:  "admin",
			Text:       "daily summary",
			SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceSchedule},
		}))
		require.NoError(t, pre.SetInterrupted(ctx, "assistant"))

		post := queue.New(queue.QueueParams{Store: s, Activity: activity, Channels: channelsFunc})
		require.NoError(t, post.LoadPersisted(ctx))

		decision := determineStartupSignal(ctx, post, channels)
		require.True(t, decision.StartNow)
		require.NotNil(t, decision.FirstMessage,
			"interrupted channel should be resumed first; persisted admin message "+
				"is drained by Queue.Next() once the agent is running")
		require.Equal(t, channel.ChannelID("assistant"), decision.FirstMessage.ChannelID)
		require.Equal(t, channel.SourceResume, decision.FirstMessage.SourceInfo.Source)
	})
}

// --- helpers ---

func setupStartupQueue(t *testing.T) (*queue.Queue, map[channel.ChannelID]channel.Channel) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	channels := map[channel.ChannelID]channel.Channel{
		"ch1": &stubResumeChannel{id: "ch1"},
	}
	activity := channel.NewActivityTracker()
	q := queue.New(queue.QueueParams{
		Store:    s,
		Activity: activity,
		Channels: func() map[channel.ChannelID]channel.Channel { return channels },
	})
	return q, channels
}
