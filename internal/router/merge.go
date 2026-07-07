package router

import (
	"context"
	"log/slog"
	"sync"

	"tclaw/internal/channel"
	"tclaw/internal/queue"
)

// mergeAgentInputs merges the durable non-user stream (schedules, notifications,
// cross-channel sends, hot-added channels) and the live user stream into the
// single channel the agent reads for an iteration.
//
// Unlike a plain fan-in, a message pulled from static but not yet delivered when
// ctx is cancelled — the agent is restarting (idle timeout, channel change,
// deploy) at that instant — is pushed to the durable queue instead of being
// dropped. Without this a schedule that fires exactly at a restart boundary
// silently vanishes: the scheduler has already removed the one-shot from its
// store, so the in-flight message is the only copy. The queue survives the
// restart and determineStartupSignal drains it on the next agent start.
//
// The live user stream keeps the same at-most-once behaviour on the static path
// only because its messages originate from the transport, which the fan-in owns;
// requeuing them here would be redundant. Normal delivery (out <- msg) is
// unchanged, so idle-wake still works: a fire arriving while the agent is idle
// flows straight through to the blocking receiver.
func mergeAgentInputs(ctx context.Context, q *queue.Queue, static, dynamic <-chan channel.TaggedMessage) <-chan channel.TaggedMessage {
	out := make(chan channel.TaggedMessage)

	var wg sync.WaitGroup
	wg.Add(2)

	// Static (durable) source: requeue an in-hand message if the iteration ends
	// before it's delivered.
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-static:
				if !ok {
					return
				}
				select {
				case out <- msg:
				case <-ctx.Done():
					// WithoutCancel so the push isn't itself cancelled by the
					// same shutdown that triggered this path.
					if err := q.Push(context.WithoutCancel(ctx), msg); err != nil {
						slog.Error("router: failed to requeue non-user message on restart",
							"channel", msg.ChannelID, "err", err)
					}
					return
				}
			}
		}
	}()

	// Live user source: plain forward.
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-dynamic:
				if !ok {
					return
				}
				select {
				case out <- msg:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}
