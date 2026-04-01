package router

import (
	"context"
	"log/slog"

	"tclaw/channel"
	"tclaw/queue"
)

// notifyParentParams holds dependencies for sending a lifecycle notification
// to a channel's parent.
type notifyParentParams struct {
	ChildName    string
	Parent       string
	Message      string
	Queue        *queue.Queue
	ChannelsFunc func() map[channel.ChannelID]channel.Channel
}

// notifyParent pushes a lifecycle message to a channel's parent via the queue.
// Best-effort: logs warnings on failure but never blocks the caller.
func notifyParent(ctx context.Context, p notifyParentParams) {
	if p.Parent == "" {
		return
	}

	parentID := resolveChannelID(p.ChannelsFunc, p.Parent)
	if parentID == "" {
		slog.Warn("parent channel not found for notification",
			"child", p.ChildName, "parent", p.Parent)
		return
	}

	if err := p.Queue.Push(ctx, channel.TaggedMessage{
		ChannelID: parentID,
		Text:      p.Message,
		SourceInfo: &channel.MessageSourceInfo{
			Source:       channel.SourceChild,
			ChildChannel: p.ChildName,
		},
	}); err != nil {
		slog.Warn("failed to notify parent channel",
			"child", p.ChildName, "parent", p.Parent, "err", err)
	}
}

// resolveChannelID finds the ChannelID for a channel by name.
func resolveChannelID(channelsFunc func() map[channel.ChannelID]channel.Channel, name string) channel.ChannelID {
	for id, ch := range channelsFunc() {
		if ch.Info().Name == name {
			return id
		}
	}
	return ""
}
