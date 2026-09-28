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

	t.Run("a first turn that fails keeps its session for the next message", func(t *testing.T) {
		h := NewHarness(t, Config{
			CommandFunc: Turn{SessionID: "stopped-by-cap", Error: &TurnError{Message: "error"}}.CommandFunc(),
		})

		h.Channel("main").Inject("first")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))
		require.Equal(t, "stopped-by-cap", h.SessionFor("main"))
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

	t.Run("hidden thinking shows no thinking line", func(t *testing.T) {
		h := NewHarness(t, Config{
			CommandFunc: Turn{Blocks: []Block{
				ThinkingBlock(""),
				TextBlock("The answer"),
			}}.CommandFunc(),
		})

		h.Channel("main").Inject("question")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))
		require.NotContains(t, h.Channel("main").ResponseText(), "💭", "a thinking block with no text must not leave a bare icon")
	})

	t.Run("thinking that ends in a blank line leaves none in the status", func(t *testing.T) {
		h := NewHarness(t, Config{
			CommandFunc: Turn{Blocks: []Block{
				ThinkingBlock("I'm going to check the files.\n\n"),
				ToolBlock("web_search", nil),
				TextBlock("Done"),
			}}.CommandFunc(),
		})

		h.Channel("main").Inject("check the files")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))
		text := h.Channel("main").ResponseText()
		// A tool line brings its own blank line above it, so the thinking adds none.
		require.Contains(t, text, "💭 I'm going to check the files.\n\n🔧 web_search", "the thinking's own blank line is dropped")
	})

	t.Run("a blank line between streamed pieces of thinking is kept", func(t *testing.T) {
		h := NewHarness(t, Config{
			CommandFunc: Turn{Blocks: []Block{
				ThinkingBlock("First.\n\n", "Second."),
				TextBlock("Done"),
			}}.CommandFunc(),
		})

		h.Channel("main").Inject("think")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))
		require.Contains(t, h.Channel("main").ResponseText(), "💭 First.\n\nSecond.\n", "newlines held back are restored once more text follows")
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

func TestHelpCommand(t *testing.T) {
	t.Run("lists the commands without running a turn", func(t *testing.T) {
		h := NewHarness(t, Config{CommandFunc: Respond("the model should not be asked")})

		h.Channel("main").Inject("help")
		h.Channel("main").Close()

		require.NoError(t, RunWithTimeout(t, h, 10*time.Second))
		require.Contains(t, h.Channel("main").LastSend(), "**stop**")
		require.Empty(t, h.TurnLog(), "help is answered by tclaw, not the model")
	})
}
