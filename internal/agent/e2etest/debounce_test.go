package e2etest

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDebounce(t *testing.T) {
	t.Run("a burst of user messages becomes a single coalesced turn", func(t *testing.T) {
		var mu sync.Mutex
		var prompts []string

		h := NewHarness(t, Config{
			DebounceWindow: 250 * time.Millisecond,
			CommandFunc: func(ctx context.Context, args, env []string, dir string) (io.ReadCloser, func() error, error) {
				mu.Lock()
				prompts = append(prompts, ExtractPrompt(args))
				mu.Unlock()
				return Respond("ok")(ctx, args, env, dir)
			},
		})

		// A photo album arrives as separate messages before the agent starts —
		// they should all land inside one debounce window and coalesce.
		h.Channel("main").Inject("photo one")
		h.Channel("main").Inject("photo two")
		h.Channel("main").Inject("photo three")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))

		mu.Lock()
		defer mu.Unlock()
		require.Len(t, prompts, 1, "burst should produce exactly one turn")
		require.Contains(t, prompts[0], "photo one")
		require.Contains(t, prompts[0], "photo two")
		require.Contains(t, prompts[0], "photo three")
		require.Len(t, h.TurnLog(), 1, "exactly one turn should have started")
	})

	t.Run("with debounce disabled each message is its own turn", func(t *testing.T) {
		var mu sync.Mutex
		var prompts []string

		h := NewHarness(t, Config{
			// DebounceWindow unset (0) — coalescing disabled, one turn per message.
			CommandFunc: func(ctx context.Context, args, env []string, dir string) (io.ReadCloser, func() error, error) {
				mu.Lock()
				prompts = append(prompts, ExtractPrompt(args))
				mu.Unlock()
				return Respond("ok")(ctx, args, env, dir)
			},
		})

		h.Channel("main").Inject("first")
		h.Channel("main").Inject("second")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))

		mu.Lock()
		defer mu.Unlock()
		require.Len(t, prompts, 2, "each message should be its own turn when debounce is off")
	})
}
