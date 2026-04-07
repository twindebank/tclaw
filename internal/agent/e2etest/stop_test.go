package e2etest

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/agent"
	"tclaw/internal/claudecli"
)

func TestStop(t *testing.T) {
	t.Run("cancels active turn", func(t *testing.T) {
		turnStarted := make(chan struct{})

		h := NewHarness(t, Config{
			AllowedTools: []claudecli.Tool{claudecli.BuiltinStop},
			CommandFunc: func(ctx context.Context, args, env []string, dir string) (io.ReadCloser, func() error, error) {
				close(turnStarted)
				// Block until cancelled by "stop".
				<-ctx.Done()
				return io.NopCloser(strings.NewReader("")), func() error { return nil }, nil
			},
		})

		h.Channel("main").Inject("long task")
		go func() {
			<-turnStarted
			h.Channel("main").Inject("stop")
			// Give the agent time to process stop and close the turn.
			time.Sleep(200 * time.Millisecond)
			// Trigger clean exit via channel change signal.
			h.CloseChannelChange()
		}()

		err := RunWithTimeout(t, h, 10*time.Second)
		require.ErrorIs(t, err, agent.ErrChannelChanged)

		// The "🤔 Thinking..." was sent before stop took effect.
		sends := h.Channel("main").Sends()
		require.True(t, len(sends) >= 1, "expected at least the thinking message")
	})
}
