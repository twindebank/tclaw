package e2etest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/channel/outbox"
	"tclaw/internal/libraries/store"
)

func TestOutbox(t *testing.T) {
	t.Run("edit coalescing through full pipeline", func(t *testing.T) {
		h := NewHarness(t, Config{
			CommandFunc: Respond("Hello, this is a long response with many streaming deltas"),
		})

		h.Channel("main").Inject("hi")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))

		edits := h.Channel("main").Edits()
		t.Logf("edits delivered: %d (coalesced from many deltas)", len(edits))
	})

	// Reproduces production panic: calling Outbox.Start() twice (once per
	// agent iteration) without stopping the previous delivery goroutines
	// causes "close of closed channel" when two goroutines defer-close the
	// same channelQueue.done channel.
	t.Run("double start panics on close of closed channel", func(t *testing.T) {
		s, err := store.NewFS(t.TempDir())
		require.NoError(t, err)

		ch := NewTestChannel(ChannelConfig{Name: "main"})
		channelMap := map[string]*TestChannel{"main": ch}

		ob := outbox.New(outbox.Params{
			Store: s,
			Channels: func() map[channel.ChannelID]channel.Channel {
				return map[channel.ChannelID]channel.Channel{
					ch.Info().ID: ch,
				}
			},
		})

		// First start — simulates first agent iteration.
		ctx1, cancel1 := context.WithCancel(context.Background())
		ob.Start(ctx1)

		// Send a message so there's a delivery goroutine and persisted state.
		_, err = ob.Send(context.Background(), ch.Info().ID, "hello")
		require.NoError(t, err)
		ob.Flush(context.Background())

		// Cancel first context — simulates agent restart (channel change, idle timeout).
		cancel1()
		// Give delivery goroutines time to notice the cancellation.
		time.Sleep(100 * time.Millisecond)

		// Second start with new context — simulates next agent iteration.
		// Before the fix, this panics with "close of closed channel" because
		// the old delivery goroutine's defer close(cq.done) races with the
		// new goroutine using the same channelQueue.
		require.NotPanics(t, func() {
			ctx2, cancel2 := context.WithCancel(context.Background())
			defer cancel2()
			ob.Start(ctx2)

			_, err := ob.Send(context.Background(), ch.Info().ID, "world")
			require.NoError(t, err)
			ob.Flush(context.Background())
			ob.Stop()
		})

		_ = channelMap // used via closure
	})
}
