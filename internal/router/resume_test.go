package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
	"tclaw/internal/queue"
)

func TestCheckAutoResume(t *testing.T) {
	t.Run("returns nil when no interrupted marker", func(t *testing.T) {
		q := setupResumeQueue(t)
		channels := map[channel.ChannelID]channel.Channel{
			"ch1": &stubResumeChannel{id: "ch1"},
		}

		msg := checkAutoResume(context.Background(), q, channels)
		require.Nil(t, msg)
	})

	t.Run("returns resume message when interrupted channel exists", func(t *testing.T) {
		q := setupResumeQueue(t)
		ctx := context.Background()
		require.NoError(t, q.SetInterrupted(ctx, "ch1"))

		channels := map[channel.ChannelID]channel.Channel{
			"ch1": &stubResumeChannel{id: "ch1"},
		}

		msg := checkAutoResume(ctx, q, channels)
		require.NotNil(t, msg)
		require.Equal(t, channel.ChannelID("ch1"), msg.ChannelID)
		require.Equal(t, channel.SourceResume, msg.SourceInfo.Source)
		require.Contains(t, msg.Text, "interrupted mid-turn")

		// Marker should be cleared.
		require.Equal(t, channel.ChannelID(""), q.InterruptedChannel())
	})

	t.Run("clears marker and returns nil when channel was deleted", func(t *testing.T) {
		q := setupResumeQueue(t)
		ctx := context.Background()
		require.NoError(t, q.SetInterrupted(ctx, "deleted-ch"))

		// Channel map does not contain the interrupted channel.
		channels := map[channel.ChannelID]channel.Channel{
			"ch1": &stubResumeChannel{id: "ch1"},
		}

		msg := checkAutoResume(ctx, q, channels)
		require.Nil(t, msg)

		// Marker should be cleared even though channel is gone.
		require.Equal(t, channel.ChannelID(""), q.InterruptedChannel())
	})

	t.Run("marker persists through queue reload", func(t *testing.T) {
		s, err := store.NewFS(t.TempDir())
		require.NoError(t, err)
		activity := channel.NewActivityTracker()
		channelsFunc := func() map[channel.ChannelID]channel.Channel {
			return map[channel.ChannelID]channel.Channel{
				"ch1": &stubResumeChannel{id: "ch1"},
			}
		}

		// Set interrupted on one queue instance.
		q1 := queue.New(queue.QueueParams{Store: s, Activity: activity, Channels: channelsFunc})
		ctx := context.Background()
		require.NoError(t, q1.SetInterrupted(ctx, "ch1"))

		// Simulate process restart: new queue from same store.
		q2 := queue.New(queue.QueueParams{Store: s, Activity: activity, Channels: channelsFunc})
		require.NoError(t, q2.LoadPersisted(ctx))

		msg := checkAutoResume(ctx, q2, channelsFunc())
		require.NotNil(t, msg)
		require.Equal(t, channel.ChannelID("ch1"), msg.ChannelID)
		require.Equal(t, channel.SourceResume, msg.SourceInfo.Source)

		// Marker cleared after resume.
		require.Equal(t, channel.ChannelID(""), q2.InterruptedChannel())
	})

	t.Run("fresh queue without LoadPersisted misses marker", func(t *testing.T) {
		// This documents the bug that was fixed: checkAutoResume must be
		// called AFTER LoadPersisted, otherwise the marker is invisible.
		s, err := store.NewFS(t.TempDir())
		require.NoError(t, err)
		activity := channel.NewActivityTracker()
		channelsFunc := func() map[channel.ChannelID]channel.Channel {
			return map[channel.ChannelID]channel.Channel{
				"ch1": &stubResumeChannel{id: "ch1"},
			}
		}

		q1 := queue.New(queue.QueueParams{Store: s, Activity: activity, Channels: channelsFunc})
		ctx := context.Background()
		require.NoError(t, q1.SetInterrupted(ctx, "ch1"))

		// New queue WITHOUT LoadPersisted — marker is not visible.
		q2 := queue.New(queue.QueueParams{Store: s, Activity: activity, Channels: channelsFunc})
		msg := checkAutoResume(ctx, q2, channelsFunc())
		require.Nil(t, msg, "fresh queue without LoadPersisted should not see the marker")

		// After LoadPersisted, the marker becomes visible.
		require.NoError(t, q2.LoadPersisted(ctx))
		msg = checkAutoResume(ctx, q2, channelsFunc())
		require.NotNil(t, msg, "after LoadPersisted, marker should be visible")
		require.Equal(t, channel.ChannelID("ch1"), msg.ChannelID)
	})
}

// --- helpers ---

func setupResumeQueue(t *testing.T) *queue.Queue {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	activity := channel.NewActivityTracker()
	return queue.New(queue.QueueParams{
		Store:    s,
		Activity: activity,
		Channels: func() map[channel.ChannelID]channel.Channel {
			return map[channel.ChannelID]channel.Channel{
				"ch1": &stubResumeChannel{id: "ch1"},
			}
		},
	})
}

type stubResumeChannel struct {
	id channel.ChannelID
}

func (c *stubResumeChannel) Info() channel.Info {
	return channel.Info{ID: c.id, Name: string(c.id)}
}
func (c *stubResumeChannel) Messages(_ context.Context) <-chan string { return nil }
func (c *stubResumeChannel) Send(_ context.Context, _ string) (channel.MessageID, error) {
	return "", nil
}
func (c *stubResumeChannel) Edit(_ context.Context, _ channel.MessageID, _ string) error { return nil }
func (c *stubResumeChannel) Done(_ context.Context) error                                { return nil }
func (c *stubResumeChannel) SplitStatusMessages() bool                                   { return false }
func (c *stubResumeChannel) Markup() channel.Markup                                      { return channel.MarkupMarkdown }
func (c *stubResumeChannel) StatusWrap() channel.StatusWrap                              { return channel.StatusWrap{} }
