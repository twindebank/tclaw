package channeltools_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/config"
	"tclaw/mcp"
	"tclaw/tool/channeltools"
)

func TestChannelSend(t *testing.T) {
	t.Run("sends message to linked channel", func(t *testing.T) {
		h, output := setupSend(t, "assistant", map[string][]config.ChannelLink{
			"assistant": {{Target: "dev", Description: "report bugs"}},
		})

		result := callTool(t, h, "channel_send", map[string]any{
			"from_channel": "assistant",
			"to_channel":   "dev",
			"message":      "found a bug in the gmail tool",
		})

		var rsp map[string]string
		require.NoError(t, json.Unmarshal(result, &rsp))
		require.Equal(t, "sent", rsp["status"])
		require.Equal(t, "assistant", rsp["from"])
		require.Equal(t, "dev", rsp["to"])

		// Verify the message was injected into the output channel.
		select {
		case msg := <-output:
			require.Equal(t, channel.ChannelID("dev-id"), msg.ChannelID)
			require.Equal(t, "found a bug in the gmail tool", msg.Text)
			require.NotNil(t, msg.SourceInfo)
			require.Equal(t, channel.SourceChannel, msg.SourceInfo.Source)
			require.Equal(t, "assistant", msg.SourceInfo.FromChannel)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for cross-channel message")
		}
	})

	t.Run("rejects spoofed from_channel", func(t *testing.T) {
		// Active channel is "dev" but agent claims to be on "assistant".
		h, _ := setupSend(t, "dev", map[string][]config.ChannelLink{
			"assistant": {{Target: "dev", Description: "report bugs"}},
		})

		err := callToolExpectError(t, h, "channel_send", map[string]any{
			"from_channel": "assistant",
			"to_channel":   "dev",
			"message":      "spoofed message",
		})
		require.Contains(t, err.Error(), "does not match active channel")
	})

	t.Run("rejects unlisted source channel", func(t *testing.T) {
		h, _ := setupSend(t, "unknown", map[string][]config.ChannelLink{
			"assistant": {{Target: "dev", Description: "report bugs"}},
		})

		err := callToolExpectError(t, h, "channel_send", map[string]any{
			"from_channel": "unknown",
			"to_channel":   "dev",
			"message":      "hello",
		})
		require.Contains(t, err.Error(), "no outbound links")
	})

	t.Run("rejects unlisted target channel", func(t *testing.T) {
		h, _ := setupSend(t, "assistant", map[string][]config.ChannelLink{
			"assistant": {{Target: "dev", Description: "report bugs"}},
		})

		err := callToolExpectError(t, h, "channel_send", map[string]any{
			"from_channel": "assistant",
			"to_channel":   "unknown",
			"message":      "hello",
		})
		require.Contains(t, err.Error(), "no link to")
	})

	t.Run("rejects missing from_channel", func(t *testing.T) {
		h, _ := setupSend(t, "assistant", map[string][]config.ChannelLink{})

		err := callToolExpectError(t, h, "channel_send", map[string]any{
			"to_channel": "dev",
			"message":    "hello",
		})
		require.Contains(t, err.Error(), "from_channel")
	})

	t.Run("rejects empty message", func(t *testing.T) {
		h, _ := setupSend(t, "assistant", map[string][]config.ChannelLink{
			"assistant": {{Target: "dev", Description: "report bugs"}},
		})

		err := callToolExpectError(t, h, "channel_send", map[string]any{
			"from_channel": "assistant",
			"to_channel":   "dev",
			"message":      "",
		})
		require.Contains(t, err.Error(), "message")
	})

	t.Run("rejects target not in active channels", func(t *testing.T) {
		output := make(chan channel.TaggedMessage, 8)
		handler := mcp.NewHandler()

		// Link exists but points to a channel not in the live map.
		channeltools.RegisterSendTool(handler, channeltools.SendDeps{
			Links: map[string][]config.ChannelLink{
				"assistant": {{Target: "missing", Description: "does not exist"}},
			},
			Output: output,
			Channels: func() map[channel.ChannelID]channel.Channel {
				return map[channel.ChannelID]channel.Channel{
					"assistant-id": &stubChannel{name: "assistant"},
				}
			},
			ActiveChannel: func() string { return "assistant" },
		})

		err := callToolExpectError(t, handler, "channel_send", map[string]any{
			"from_channel": "assistant",
			"to_channel":   "missing",
			"message":      "hello",
		})
		require.Contains(t, err.Error(), "not found in active channels")
	})
}

// --- helpers ---

func setupSend(t *testing.T, activeChannel string, links map[string][]config.ChannelLink) (*mcp.Handler, chan channel.TaggedMessage) {
	t.Helper()
	output := make(chan channel.TaggedMessage, 8)
	handler := mcp.NewHandler()

	channelMap := map[channel.ChannelID]channel.Channel{
		"assistant-id": &stubChannel{name: "assistant"},
		"dev-id":       &stubChannel{name: "dev"},
	}

	channeltools.RegisterSendTool(handler, channeltools.SendDeps{
		Links:  links,
		Output: output,
		Channels: func() map[channel.ChannelID]channel.Channel {
			return channelMap
		},
		ActiveChannel: func() string { return activeChannel },
	})

	return handler, output
}

// stubChannel implements channel.Channel for testing name resolution.
type stubChannel struct {
	name string
}

func (s *stubChannel) Info() channel.Info {
	return channel.Info{ID: channel.ChannelID(s.name + "-id"), Name: s.name, Type: channel.TypeSocket}
}

func (s *stubChannel) Messages(context.Context) <-chan string                  { return make(chan string) }
func (s *stubChannel) Send(context.Context, string) (channel.MessageID, error) { return "", nil }
func (s *stubChannel) Edit(context.Context, channel.MessageID, string) error   { return nil }
func (s *stubChannel) Done(context.Context) error                              { return nil }
func (s *stubChannel) SplitStatusMessages() bool                               { return false }
func (s *stubChannel) Markup() channel.Markup                                  { return channel.MarkupMarkdown }
func (s *stubChannel) StatusWrap() channel.StatusWrap                          { return channel.StatusWrap{} }
