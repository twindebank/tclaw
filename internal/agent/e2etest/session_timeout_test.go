package e2etest

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionTimeout(t *testing.T) {
	t.Run("fresh-session notice fires when resolver drops the cached session", func(t *testing.T) {
		// Seed the channel with an active session, then have the resolver
		// return "" — the agent should notice and post the fresh-session
		// notice before kicking the next turn off without --resume.
		h := NewHarness(t, Config{
			CommandFunc: Respond("ok"),
			Sessions:    map[string]string{"main": "stale-from-yesterday"},
			SessionResolver: func(name string) string {
				return ""
			},
		})

		h.Channel("main").Inject("any new message")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))

		sends := h.Channel("main").Sends()
		var sawNotice bool
		var texts []string
		for _, s := range sends {
			texts = append(texts, s.Text)
			if strings.Contains(s.Text, "Idle timeout reached") {
				sawNotice = true
			}
		}
		require.True(t, sawNotice, "expected fresh-session notice in channel output, got %v", texts)
	})

	t.Run("no notice when resolver keeps the session alive", func(t *testing.T) {
		h := NewHarness(t, Config{
			CommandFunc: Respond("ok"),
			Sessions:    map[string]string{"main": "still-warm"},
			SessionResolver: func(name string) string {
				return "still-warm"
			},
		})

		h.Channel("main").Inject("any new message")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))

		for _, s := range h.Channel("main").Sends() {
			require.NotContains(t, s.Text, "Idle timeout reached",
				"resolver kept the session alive but agent still sent a fresh-session notice")
		}
	})
}
