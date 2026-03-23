package router

import (
	"context"
	"log/slog"
	"time"

	"tclaw/channel"
)

const (
	// drainInterval is how often the pending message drain goroutine checks
	// the queue for deliverable messages.
	drainInterval = 15 * time.Second

	// delayedPrefix is prepended to messages that are delivered after their
	// timeout expired (target was still busy).
	delayedPrefix = "[delayed] "
)

// drainPendingMessages runs at user lifetime and periodically checks the
// pending message queue. For each message: if the target channel is free,
// deliver it. If the message has expired, deliver it anyway with a prefix.
// Errors are always logged, never silently swallowed.
func drainPendingMessages(
	ctx context.Context,
	store *channel.PendingStore,
	tracker *channel.ActivityTracker,
	output chan<- channel.TaggedMessage,
	channels func() map[channel.ChannelID]channel.Channel,
) {
	ticker := time.NewTicker(drainInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			drainOnce(ctx, store, tracker, output, channels)
		}
	}
}

func drainOnce(
	ctx context.Context,
	store *channel.PendingStore,
	tracker *channel.ActivityTracker,
	output chan<- channel.TaggedMessage,
	channels func() map[channel.ChannelID]channel.Channel,
) {
	pending, err := store.List(ctx)
	if err != nil {
		slog.Error("failed to read pending messages", "err", err)
		return
	}

	now := time.Now()
	for _, msg := range pending {
		expired := now.After(msg.ExpiresAt)
		busy := tracker != nil && tracker.IsBusy(msg.ToChannel)

		if busy && !expired {
			// Target is busy and we haven't timed out — skip for now.
			continue
		}

		// Resolve the target channel name to an ID.
		chMap := channels()
		if chMap == nil {
			slog.Error("no channels available for pending message delivery", "pending_id", msg.ID)
			continue
		}

		var targetID channel.ChannelID
		found := false
		for _, ch := range chMap {
			if ch.Info().Name == msg.ToChannel {
				targetID = ch.Info().ID
				found = true
				break
			}
		}
		if !found {
			// Channel might have been deleted — log and remove the message.
			slog.Error("target channel not found for pending message, removing",
				"pending_id", msg.ID, "to_channel", msg.ToChannel)
			if removeErr := store.Remove(ctx, msg.ID); removeErr != nil {
				slog.Error("failed to remove undeliverable pending message",
					"pending_id", msg.ID, "err", removeErr)
			}
			continue
		}

		// Prepend [delayed] if we're delivering after the timeout.
		text := msg.Message
		if expired {
			text = delayedPrefix + text
		}

		tagged := channel.TaggedMessage{
			ChannelID: targetID,
			Text:      text,
			SourceInfo: &channel.MessageSourceInfo{
				Source:      channel.SourceChannel,
				FromChannel: msg.FromChannel,
			},
		}

		select {
		case output <- tagged:
			slog.Info("delivered pending message",
				"pending_id", msg.ID,
				"from", msg.FromChannel,
				"to", msg.ToChannel,
				"expired", expired,
				"queued_for", time.Since(msg.QueuedAt).Round(time.Second))
		default:
			// Buffer full — retry next tick rather than dropping.
			slog.Warn("message buffer full, retrying pending message next tick",
				"pending_id", msg.ID, "to_channel", msg.ToChannel)
			continue
		}

		// Successfully delivered — remove from the queue.
		if removeErr := store.Remove(ctx, msg.ID); removeErr != nil {
			slog.Error("failed to remove delivered pending message",
				"pending_id", msg.ID, "err", removeErr)
		}
	}
}
