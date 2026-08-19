package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestNewChannelFileSender(t *testing.T) {
	file := channel.SendFileParams{Filename: "guide.pdf", Content: []byte("%PDF-1.4")}

	t.Run("delivers to the channel the turn is running on", func(t *testing.T) {
		target := &fileCapableChannel{name: "homeassistant"}
		send := newChannelFileSender(channelFileSenderParams{
			ActiveChannel: func() string { return "homeassistant" },
			Channels: func() map[channel.ChannelID]channel.Channel {
				return map[channel.ChannelID]channel.Channel{
					"other": &plainChannel{name: "phone"},
					"live":  target,
				}
			},
		})

		id, err := send(context.Background(), file)

		require.NoError(t, err)
		require.Equal(t, channel.MessageID("sent-1"), id)
		require.Equal(t, "guide.pdf", target.got.Filename, "the right channel received it")
	})

	t.Run("reports when no channel is active", func(t *testing.T) {
		send := newChannelFileSender(channelFileSenderParams{
			ActiveChannel: func() string { return "" },
			Channels:      func() map[channel.ChannelID]channel.Channel { return nil },
		})

		_, err := send(context.Background(), file)

		require.Error(t, err)
		require.Equal(t, "no active channel to send the file to", err.Error())
	})

	t.Run("reports when the active channel is not live", func(t *testing.T) {
		send := newChannelFileSender(channelFileSenderParams{
			ActiveChannel: func() string { return "homeassistant" },
			Channels:      func() map[channel.ChannelID]channel.Channel { return nil },
		})

		_, err := send(context.Background(), file)

		require.Error(t, err)
		require.Equal(t, `channel "homeassistant" is not live, so nothing can be sent to it`, err.Error())
	})

	t.Run("names the transport when it cannot carry a file", func(t *testing.T) {
		send := newChannelFileSender(channelFileSenderParams{
			ActiveChannel: func() string { return "desk" },
			Channels: func() map[channel.ChannelID]channel.Channel {
				return map[channel.ChannelID]channel.Channel{"live": &plainChannel{name: "desk"}}
			},
		})

		_, err := send(context.Background(), file)

		require.Error(t, err)
		require.Equal(t, `channel "desk" is a socket channel, which cannot carry a file`, err.Error())
	})
}

// --- helpers ---

// plainChannel implements Channel but not FileSender.
type plainChannel struct {
	name string
}

func (c *plainChannel) Info() channel.Info {
	return channel.Info{Name: c.name, Type: channel.TypeSocket}
}
func (c *plainChannel) Messages(context.Context) <-chan string { return nil }
func (c *plainChannel) Send(context.Context, string, channel.SendOpts) (channel.MessageID, error) {
	return "", nil
}
func (c *plainChannel) Edit(context.Context, channel.MessageID, string) error { return nil }
func (c *plainChannel) Done(context.Context) error                            { return nil }
func (c *plainChannel) SplitStatusMessages() bool                             { return false }
func (c *plainChannel) Markup() channel.Markup                                { return channel.MarkupMarkdown }
func (c *plainChannel) StatusWrap() channel.StatusWrap                        { return channel.StatusWrap{} }

type fileCapableChannel struct {
	plainChannel
	name string
	got  channel.SendFileParams
}

func (c *fileCapableChannel) Info() channel.Info {
	return channel.Info{Name: c.name, Type: channel.TypeTelegram}
}

func (c *fileCapableChannel) SendFile(_ context.Context, p channel.SendFileParams) (channel.MessageID, error) {
	c.got = p
	return channel.MessageID("sent-1"), nil
}
