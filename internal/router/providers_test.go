package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/config"
)

func TestInjectInitialMessages(t *testing.T) {
	t.Run("delivers the configured message and clears the config entry", func(t *testing.T) {
		cw := setupProvidersConfig(t, []config.Channel{
			{
				Name:           "homeassistant",
				Type:           channel.TypeTelegram,
				Description:    "Home Assistant automation",
				InitialMessage: "Hello — I'm ready to help with Home Assistant.",
			},
		})

		chMap := map[channel.ChannelID]channel.Channel{
			"telegram:homeassistant": &stubResumeChannel{id: "telegram:homeassistant"},
		}
		// stubResumeChannel.Info().Name returns the ID string; override to match.
		chMap["telegram:homeassistant"] = &stubNamedChannel{
			id:   "telegram:homeassistant",
			name: "homeassistant",
		}

		output := make(chan channel.TaggedMessage, 1)
		injectInitialMessages(context.Background(), testUserID, cw, chMap, output)

		select {
		case msg := <-output:
			require.Equal(t, channel.ChannelID("telegram:homeassistant"), msg.ChannelID)
			require.Equal(t, "Hello — I'm ready to help with Home Assistant.", msg.Text)
		default:
			t.Fatal("expected an initial_message on the output channel, got nothing")
		}

		channels, err := cw.ReadChannels(testUserID)
		require.NoError(t, err)
		require.Len(t, channels, 1)
		require.Empty(t, channels[0].InitialMessage, "initial_message must be cleared after delivery")
	})

	t.Run("skips channels that have no initial_message", func(t *testing.T) {
		cw := setupProvidersConfig(t, []config.Channel{
			{Name: "plain", Type: channel.TypeSocket, Description: "no message"},
		})
		chMap := map[channel.ChannelID]channel.Channel{
			"plain": &stubNamedChannel{id: "plain", name: "plain"},
		}
		output := make(chan channel.TaggedMessage, 1)

		injectInitialMessages(context.Background(), testUserID, cw, chMap, output)

		require.Empty(t, output, "no initial_message means nothing to deliver")
	})

	t.Run("skips with warning when channel is not in chMap yet", func(t *testing.T) {
		cw := setupProvidersConfig(t, []config.Channel{
			{
				Name:           "late",
				Type:           channel.TypeTelegram,
				Description:    "not yet built",
				InitialMessage: "hi",
			},
		})
		chMap := map[channel.ChannelID]channel.Channel{}
		output := make(chan channel.TaggedMessage, 1)

		injectInitialMessages(context.Background(), testUserID, cw, chMap, output)

		require.Empty(t, output)

		// Config must be untouched — we didn't deliver, so we must not clear.
		channels, err := cw.ReadChannels(testUserID)
		require.NoError(t, err)
		require.Len(t, channels, 1)
		require.Equal(t, "hi", channels[0].InitialMessage,
			"initial_message must not be cleared when delivery was skipped")
	})

	t.Run("delivers only once — second call with cleared config is a no-op", func(t *testing.T) {
		cw := setupProvidersConfig(t, []config.Channel{
			{
				Name:           "homeassistant",
				Type:           channel.TypeTelegram,
				Description:    "HA",
				InitialMessage: "welcome",
			},
		})
		chMap := map[channel.ChannelID]channel.Channel{
			"telegram:homeassistant": &stubNamedChannel{id: "telegram:homeassistant", name: "homeassistant"},
		}
		output := make(chan channel.TaggedMessage, 2)

		injectInitialMessages(context.Background(), testUserID, cw, chMap, output)
		injectInitialMessages(context.Background(), testUserID, cw, chMap, output)

		require.Len(t, output, 1, "initial_message must fire exactly once across restarts")
	})
}

// --- helpers ---

func setupProvidersConfig(t *testing.T, channels []config.Channel) *config.Writer {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "tclaw.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("local:\n  users:\n    - id: testuser\n      channels: []\n"), 0o644))

	cw := config.NewWriter(configPath, config.EnvLocal)
	for _, ch := range channels {
		require.NoError(t, cw.AddChannel(testUserID, ch))
	}
	return cw
}

// stubNamedChannel is a Channel with a configurable Name so we can match
// injectInitialMessages's lookup (which joins ChannelID → channel name).
type stubNamedChannel struct {
	id   channel.ChannelID
	name string
}

func (c *stubNamedChannel) Info() channel.Info {
	return channel.Info{ID: c.id, Name: c.name}
}
func (c *stubNamedChannel) Messages(_ context.Context) <-chan string { return nil }
func (c *stubNamedChannel) Send(_ context.Context, _ string, _ channel.SendOpts) (channel.MessageID, error) {
	return "", nil
}
func (c *stubNamedChannel) Edit(_ context.Context, _ channel.MessageID, _ string) error { return nil }
func (c *stubNamedChannel) Done(_ context.Context) error                                { return nil }
func (c *stubNamedChannel) SplitStatusMessages() bool                                   { return false }
func (c *stubNamedChannel) Markup() channel.Markup                                      { return channel.MarkupMarkdown }
func (c *stubNamedChannel) StatusWrap() channel.StatusWrap                              { return channel.StatusWrap{} }
