package e2etest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBasicFlow(t *testing.T) {
	t.Run("single message response", func(t *testing.T) {
		h := NewHarness(t, Config{
			CommandFunc: Respond("Hello, world!"),
		})

		h.Channel("main").Inject("hi")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))
		require.Contains(t, h.Channel("main").ResponseText(), "Hello, world!")
	})

	t.Run("session ID persisted across turns", func(t *testing.T) {
		h := NewHarness(t, Config{
			CommandFunc: Turn{SessionID: "persist-me", Blocks: []Block{TextBlock("ok")}}.CommandFunc(),
		})

		h.Channel("main").Inject("first")
		h.Channel("main").Inject("second")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))
		require.Equal(t, "persist-me", h.SessionFor("main"))
	})

	t.Run("thinking and tool blocks before response", func(t *testing.T) {
		h := NewHarness(t, Config{
			CommandFunc: Turn{Blocks: []Block{
				ThinkingBlock("Let me consider this..."),
				ToolBlock("web_search", nil),
				TextBlock("Here are the results"),
			}}.CommandFunc(),
		})

		h.Channel("main").Inject("search for something")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))
		require.Contains(t, h.Channel("main").ResponseText(), "Here are the results")
	})

	t.Run("turn log records channel names", func(t *testing.T) {
		h := NewHarness(t, Config{
			CommandFunc: Respond("ok"),
		})

		h.Channel("main").Inject("hi")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))

		log := h.TurnLog()
		require.NotEmpty(t, log)
		require.Equal(t, "main", log[0].ChannelName)
	})
}
