package router

import (
	"context"
	"log/slog"

	"tclaw/internal/channel"
	"tclaw/internal/queue"
)

// checkAutoResume checks whether the queue has an interrupted channel marker
// from a previous agent session (e.g. force-killed during a channel change).
// If the interrupted channel still exists, it returns a synthetic resume
// message and clears the marker. If the channel was deleted, it clears the
// marker and returns nil. Returns nil when there is no interrupted marker.
func checkAutoResume(ctx context.Context, q *queue.Queue, channels map[channel.ChannelID]channel.Channel) *channel.TaggedMessage {
	interruptedCh := q.InterruptedChannel()
	if interruptedCh == "" {
		return nil
	}

	if _, exists := channels[interruptedCh]; !exists {
		slog.Warn("interrupted channel no longer exists, clearing marker", "channel", interruptedCh)
		if err := q.ClearInterrupted(ctx); err != nil {
			slog.Error("failed to clear interrupted marker for missing channel", "err", err)
		}
		return nil
	}

	slog.Info("auto-resuming interrupted channel after restart", "channel", interruptedCh)
	if err := q.ClearInterrupted(ctx); err != nil {
		slog.Error("failed to clear interrupted marker after auto-resume", "err", err)
	}

	return &channel.TaggedMessage{
		ChannelID:  interruptedCh,
		Text:       "[SYSTEM: You were interrupted mid-turn by a restart. Review your conversation history and continue what you were doing. If you were waiting for user input, let the user know you're back.]",
		SourceInfo: &channel.MessageSourceInfo{Source: channel.SourceResume},
	}
}
