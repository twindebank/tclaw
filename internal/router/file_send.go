package router

import (
	"context"
	"fmt"

	"tclaw/internal/channel"
)

type channelFileSenderParams struct {
	ActiveChannel func() string
	Channels      func() map[channel.ChannelID]channel.Channel
}

// newChannelFileSender returns the hook document tools call to deliver a file
// to whichever channel the turn is running on.
func newChannelFileSender(params channelFileSenderParams) func(context.Context, channel.SendFileParams) (channel.MessageID, error) {
	return func(ctx context.Context, file channel.SendFileParams) (channel.MessageID, error) {
		if params.ActiveChannel == nil || params.Channels == nil {
			return "", fmt.Errorf("channel lookup is not wired up, so nothing can be sent")
		}
		chName := params.ActiveChannel()
		if chName == "" {
			return "", fmt.Errorf("no active channel to send the file to")
		}

		var target channel.Channel
		for _, ch := range params.Channels() {
			if ch.Info().Name == chName {
				target = ch
				break
			}
		}
		if target == nil {
			return "", fmt.Errorf("channel %q is not live, so nothing can be sent to it", chName)
		}

		sender, ok := target.(channel.FileSender)
		if !ok {
			return "", fmt.Errorf("channel %q is a %s channel, which cannot carry a file", chName, target.Info().Type)
		}

		id, err := sender.SendFile(ctx, file)
		if err != nil {
			return "", fmt.Errorf("send %q to %q: %w", file.Filename, chName, err)
		}
		return id, nil
	}
}
