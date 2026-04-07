package e2etest

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/agent"
)

func TestLifecycle(t *testing.T) {
	t.Run("channel change exits agent", func(t *testing.T) {
		h := NewHarness(t, Config{
			CommandFunc: Respond("ok"),
		})

		h.Channel("main").Inject("hi")
		go func() {
			// Wait for the turn to complete then trigger channel change.
			h.Channel("main").WaitForDone(t, 5*time.Second)
			h.CloseChannelChange()
		}()

		err := RunWithTimeout(t, h, 10*time.Second)
		require.ErrorIs(t, err, agent.ErrChannelChanged)
	})

	t.Run("rate limit retries", func(t *testing.T) {
		var attempts int32

		h := NewHarness(t, Config{
			CommandFunc: func(ctx context.Context, args, env []string, dir string) (io.ReadCloser, func() error, error) {
				n := atomic.AddInt32(&attempts, 1)
				if n == 1 {
					return Turn{Error: &TurnError{RateLimited: true}}.CommandFunc()(ctx, args, env, dir)
				}
				return Respond("success after retry")(ctx, args, env, dir)
			},
		})

		h.Channel("main").Inject("query")
		h.Channel("main").Close()

		// Rate limit retry has a 30s initial delay — use a generous timeout.
		require.NoError(t, RunWithTimeout(t, h, 60*time.Second))

		require.Equal(t, int32(2), atomic.LoadInt32(&attempts))
		require.Contains(t, h.Channel("main").ResponseText(), "success after retry")
	})

	t.Run("scripted responses per message", func(t *testing.T) {
		h := NewHarness(t, Config{
			CommandFunc: Scripted([]Script{
				{Match: MatchPrompt("hello"), Respond: Respond("Hi there!")},
				{Match: MatchPrompt("goodbye"), Respond: Respond("See you later!")},
			}, Respond("unknown")),
		})

		h.Channel("main").Inject("hello")
		h.Channel("main").Inject("goodbye")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))

		// Both turns completed.
		require.Len(t, h.TurnLog(), 2)
	})
}
