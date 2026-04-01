package agent

import (
	"context"
	"log/slog"
	"time"

	"tclaw/channel"
)

// restartNoticeTimeout is how long to wait when sending the restart notice
// after a channel change. Uses a detached context so it works even after
// the parent context is force-cancelled.
const restartNoticeTimeout = 5 * time.Second

// checkChannelChanged checks whether the router signalled a channel change
// during the current turn. If so, it sends a restart notice to the active
// channel and returns true. Returns false if no channel change occurred.
//
// The restart notice uses a detached context (not the parent agent context)
// so it can still send even after a force-kill cancels the parent.
func checkChannelChanged(changeCh <-chan struct{}, ch channel.Channel) bool {
	if changeCh == nil {
		return false
	}

	select {
	case <-changeCh:
	default:
		return false
	}

	if ch != nil {
		noticeCtx, cancel := context.WithTimeout(context.Background(), restartNoticeTimeout)
		defer cancel()

		if _, err := ch.Send(noticeCtx, "🔄 Restarting to apply channel changes..."); err != nil {
			slog.Error("failed to send restart notice", "err", err)
		}
		if err := ch.Done(noticeCtx); err != nil {
			slog.Error("failed to close turn after restart notice", "err", err)
		}
	}

	return true
}
