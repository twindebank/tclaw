package queue_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
	"tclaw/internal/queue"
)

func TestQueue(t *testing.T) {
	t.Run("returns user message immediately", func(t *testing.T) {
		q, _ := setup(t)
		ctx := context.Background()
		input := make(chan channel.TaggedMessage, 1)

		input <- channel.TaggedMessage{
			ChannelID: "ch1",
			Text:      "hello",
			SourceInfo: &channel.MessageSourceInfo{
				Source: channel.SourceUser,
			},
		}

		msg, err := q.Next(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "hello", msg.Text)
		require.Equal(t, channel.SourceUser, msg.SourceInfo.Source)
	})

	t.Run("user message dequeues ahead of non-user", func(t *testing.T) {
		q, _ := setup(t)
		ctx := context.Background()
		input := make(chan channel.TaggedMessage)

		// Push a schedule message first, then a user message.
		require.NoError(t, q.Push(ctx, channel.TaggedMessage{
			ChannelID:  "ch1",
			Text:       "scheduled task",
			SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceSchedule},
		}))
		require.NoError(t, q.Push(ctx, channel.TaggedMessage{
			ChannelID:  "ch1",
			Text:       "user says hi",
			SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceUser},
		}))

		msg, err := q.Next(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "user says hi", msg.Text)
	})

	t.Run("non-user message blocks while channel is busy", func(t *testing.T) {
		q, activity := setup(t)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		input := make(chan channel.TaggedMessage)

		// Mark channel as busy (both processing and recent message).
		activity.MessageReceived("main")
		activity.TurnStarted("main")

		// Push a schedule message.
		require.NoError(t, q.Push(ctx, channel.TaggedMessage{
			ChannelID:  "ch1",
			Text:       "scheduled task",
			SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceSchedule},
		}))

		// Next should block — channel is busy.
		done := make(chan channel.TaggedMessage, 1)
		go func() {
			msg, err := q.Next(ctx, input)
			if err == nil {
				done <- msg
			}
		}()

		select {
		case <-done:
			t.Fatal("expected Next to block while channel is busy")
		case <-time.After(100 * time.Millisecond):
		}

		// Push a user message — it should jump ahead of the schedule message.
		require.NoError(t, q.Push(ctx, channel.TaggedMessage{
			ChannelID:  "ch1",
			Text:       "user says hi",
			SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceUser},
		}))

		select {
		case msg := <-done:
			require.Equal(t, "user says hi", msg.Text)
		case <-ctx.Done():
			t.Fatal("timed out waiting for user message")
		}
	})

	t.Run("non-user message dequeues when channel is not busy", func(t *testing.T) {
		q, _ := setup(t)
		ctx := context.Background()
		input := make(chan channel.TaggedMessage)

		// Channel is not busy — schedule message should dequeue immediately.
		require.NoError(t, q.Push(ctx, channel.TaggedMessage{
			ChannelID:  "ch1",
			Text:       "scheduled task",
			SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceSchedule},
		}))

		msg, err := q.Next(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "scheduled task", msg.Text)
	})

	t.Run("resume message dequeues immediately", func(t *testing.T) {
		q, _ := setup(t)
		ctx := context.Background()
		input := make(chan channel.TaggedMessage)

		require.NoError(t, q.Push(ctx, channel.TaggedMessage{
			ChannelID:  "ch1",
			Text:       "resume",
			SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceResume},
		}))

		msg, err := q.Next(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "resume", msg.Text)
	})

	t.Run("context cancellation returns error", func(t *testing.T) {
		q, _ := setup(t)
		ctx, cancel := context.WithCancel(context.Background())
		input := make(chan channel.TaggedMessage)

		cancel()
		_, err := q.Next(ctx, input)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("closed input returns ErrInputClosed", func(t *testing.T) {
		q, _ := setup(t)
		ctx := context.Background()
		input := make(chan channel.TaggedMessage)
		close(input)

		_, err := q.Next(ctx, input)
		require.Error(t, err)
		require.ErrorIs(t, err, queue.ErrInputClosed)
	})

	t.Run("messages survive reload", func(t *testing.T) {
		s, err := store.NewFS(t.TempDir())
		require.NoError(t, err)
		activity := channel.NewActivityTracker()
		channels := channelsFunc()

		// Create queue, push, then create a new queue from same store.
		q1 := queue.New(queue.QueueParams{Store: s, Activity: activity, Channels: channels})
		ctx := context.Background()
		require.NoError(t, q1.Push(ctx, channel.TaggedMessage{
			ChannelID:  "ch1",
			Text:       "persisted msg",
			SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceSchedule},
		}))
		require.Equal(t, 1, q1.Len())

		// New queue from same store.
		q2 := queue.New(queue.QueueParams{Store: s, Activity: activity, Channels: channels})
		require.NoError(t, q2.LoadPersisted(ctx))
		require.Equal(t, 1, q2.Len())

		input := make(chan channel.TaggedMessage)
		msg, err := q2.Next(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "persisted msg", msg.Text)
	})

	t.Run("interrupted channel survives reload", func(t *testing.T) {
		s, err := store.NewFS(t.TempDir())
		require.NoError(t, err)
		activity := channel.NewActivityTracker()
		channels := channelsFunc()

		q1 := queue.New(queue.QueueParams{Store: s, Activity: activity, Channels: channels})
		ctx := context.Background()
		require.NoError(t, q1.SetInterrupted(ctx, "ch1"))

		q2 := queue.New(queue.QueueParams{Store: s, Activity: activity, Channels: channels})
		require.NoError(t, q2.LoadPersisted(ctx))
		require.Equal(t, channel.ChannelID("ch1"), q2.InterruptedChannel())
	})

	t.Run("set and clear interrupted", func(t *testing.T) {
		q, _ := setup(t)
		ctx := context.Background()

		require.Equal(t, channel.ChannelID(""), q.InterruptedChannel())

		require.NoError(t, q.SetInterrupted(ctx, "ch1"))
		require.Equal(t, channel.ChannelID("ch1"), q.InterruptedChannel())

		require.NoError(t, q.ClearInterrupted(ctx))
		require.Equal(t, channel.ChannelID(""), q.InterruptedChannel())
	})

	t.Run("coalesces same-channel user messages within the debounce window", func(t *testing.T) {
		q := setupDebounce(t, 30*time.Millisecond)
		ctx := context.Background()
		input := make(chan channel.TaggedMessage)

		for _, text := range []string{"photo one", "photo two", "photo three"} {
			require.NoError(t, q.Push(ctx, userMsg("ch1", text)))
		}

		msg, err := q.Next(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "photo one\n\nphoto two\n\nphoto three", msg.Text)
		require.Equal(t, channel.ChannelID("ch1"), msg.ChannelID)
		require.Equal(t, channel.SourceUser, msg.SourceInfo.Source)
		require.Equal(t, 0, q.Len(), "queue should be drained after coalescing")
	})

	t.Run("does not merge messages from different channels", func(t *testing.T) {
		q := setupDebounce(t, 30*time.Millisecond)
		ctx := context.Background()
		input := make(chan channel.TaggedMessage)

		require.NoError(t, q.Push(ctx, userMsg("ch1", "from one")))
		require.NoError(t, q.Push(ctx, userMsg("ch2", "from two")))

		first, err := q.Next(ctx, input)
		require.NoError(t, err)
		second, err := q.Next(ctx, input)
		require.NoError(t, err)

		require.ElementsMatch(t, []string{"from one", "from two"}, []string{first.Text, second.Text})
		require.NotEqual(t, first.ChannelID, second.ChannelID)
	})

	t.Run("control command is never batched", func(t *testing.T) {
		q := setupDebounce(t, 30*time.Millisecond)
		ctx := context.Background()
		input := make(chan channel.TaggedMessage)

		// A lone control command is returned immediately, on its own turn.
		require.NoError(t, q.Push(ctx, userMsg("ch1", "stop")))
		msg, err := q.Next(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "stop", msg.Text)

		// A stop queued after an album leaves the batch intact; the batch turn runs
		// first, then the stop is handled on the following Next.
		require.NoError(t, q.Push(ctx, userMsg("ch1", "photo one")))
		require.NoError(t, q.Push(ctx, userMsg("ch1", "photo two")))
		require.NoError(t, q.Push(ctx, userMsg("ch1", "stop")))

		batch, err := q.Next(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "photo one\n\nphoto two", batch.Text)

		after, err := q.Next(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "stop", after.Text)
	})

	t.Run("reset-on-arrival coalesces siblings that trickle in", func(t *testing.T) {
		q := setupDebounce(t, 40*time.Millisecond)
		ctx := context.Background()
		input := make(chan channel.TaggedMessage, 1)

		require.NoError(t, q.Push(ctx, userMsg("ch1", "first")))

		// Deliver a sibling through the input channel partway through the window;
		// the reset-on-arrival window should keep it in the same batch.
		go func() {
			time.Sleep(15 * time.Millisecond)
			input <- userMsg("ch1", "second")
		}()

		msg, err := q.Next(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "first\n\nsecond", msg.Text)
		require.Equal(t, 0, q.Len())
	})

	t.Run("debounce window of zero returns one message per Next", func(t *testing.T) {
		s, err := store.NewFS(t.TempDir())
		require.NoError(t, err)
		// DebounceWindow unset (0) — coalescing disabled, one message per Next.
		q := queue.New(queue.QueueParams{
			Store:    s,
			Activity: channel.NewActivityTracker(),
			Channels: multiChannelsFunc(),
		})
		ctx := context.Background()
		input := make(chan channel.TaggedMessage)

		require.NoError(t, q.Push(ctx, userMsg("ch1", "one")))
		require.NoError(t, q.Push(ctx, userMsg("ch1", "two")))

		first, err := q.Next(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "one", first.Text)

		second, err := q.Next(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "two", second.Text)
	})
}

// --- helpers ---

func setup(t *testing.T) (*queue.Queue, *channel.ActivityTracker) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	activity := channel.NewActivityTracker()
	q := queue.New(queue.QueueParams{
		Store:    s,
		Activity: activity,
		Channels: channelsFunc(),
	})
	return q, activity
}

func channelsFunc() func() map[channel.ChannelID]channel.Channel {
	return func() map[channel.ChannelID]channel.Channel {
		return map[channel.ChannelID]channel.Channel{
			"ch1": &mockChannel{name: "main", id: "ch1"},
		}
	}
}

// setupDebounce builds a queue with debouncing enabled and "stop" treated as a
// control command, so tests can exercise coalescing and the control carve-out.
func setupDebounce(t *testing.T, window time.Duration) *queue.Queue {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return queue.New(queue.QueueParams{
		Store:          s,
		Activity:       channel.NewActivityTracker(),
		Channels:       multiChannelsFunc(),
		DebounceWindow: window,
		IsControlMessage: func(m channel.TaggedMessage) bool {
			return strings.EqualFold(strings.TrimSpace(m.Text), "stop")
		},
	})
}

func multiChannelsFunc() func() map[channel.ChannelID]channel.Channel {
	return func() map[channel.ChannelID]channel.Channel {
		return map[channel.ChannelID]channel.Channel{
			"ch1": &mockChannel{name: "main", id: "ch1"},
			"ch2": &mockChannel{name: "second", id: "ch2"},
		}
	}
}

func userMsg(chID channel.ChannelID, text string) channel.TaggedMessage {
	return channel.TaggedMessage{
		ChannelID:  chID,
		Text:       text,
		SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceUser},
	}
}

type mockChannel struct {
	name string
	id   channel.ChannelID
}

func (c *mockChannel) Info() channel.Info {
	return channel.Info{ID: c.id, Name: c.name}
}
func (c *mockChannel) Messages(_ context.Context) <-chan string { return nil }
func (c *mockChannel) Send(_ context.Context, _ string, _ channel.SendOpts) (channel.MessageID, error) {
	return "", nil
}
func (c *mockChannel) Edit(_ context.Context, _ channel.MessageID, _ string) error { return nil }
func (c *mockChannel) Done(_ context.Context) error                                { return nil }
func (c *mockChannel) SplitStatusMessages() bool                                   { return false }
func (c *mockChannel) Markup() channel.Markup                                      { return channel.MarkupMarkdown }
func (c *mockChannel) StatusWrap() channel.StatusWrap                              { return channel.StatusWrap{} }
