package channel

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultIdleTimeout is how long after a message is received a channel is still
// considered busy, even if the agent turn has completed. This accounts for
// back-and-forth conversations where the user may reply again shortly.
const DefaultIdleTimeout = 3 * time.Minute

// ActivityTracker tracks per-channel activity so other channels can check
// whether a channel is busy before sending cross-channel messages.
//
// A channel is considered busy if:
//   - An agent turn is currently processing a message on it, OR
//   - A message was received within the last idleTimeout (conversation cooldown)
//
// When a RuntimeStateStore is provided, lastMessageAt is persisted so
// ephemeral cleanup decisions survive process restarts.
type ActivityTracker struct {
	mu           sync.Mutex
	entries      map[string]*channelActivity
	runtimeState *RuntimeStateStore
}

type channelActivity struct {
	// processing is true while an agent turn is actively running for this channel.
	processing bool

	// lastMessageAt is the time the most recent inbound message was received.
	lastMessageAt time.Time

	// lastMessageSource is who sent the most recent message.
	lastMessageSource MessageSource

	// idleWaiters are closed when the channel transitions from busy to idle.
	// Consumers call NotifyIdle() to get a waiter channel, then select on it.
	idleWaiters []chan struct{}

	// idleTimer fires after the idle timeout to signal waiters.
	// Nil when no timer is pending.
	idleTimer *time.Timer
}

// NewActivityTracker returns an initialised ActivityTracker with no persistence.
func NewActivityTracker() *ActivityTracker {
	return &ActivityTracker{
		entries: make(map[string]*channelActivity),
	}
}

// NewPersistedActivityTracker returns a tracker that persists lastMessageAt
// to the given RuntimeStateStore. On construction it loads persisted timestamps
// for the given channel names so IsBusyWithTimeout works correctly after restart.
func NewPersistedActivityTracker(ctx context.Context, runtimeState *RuntimeStateStore, channelNames []string) *ActivityTracker {
	t := &ActivityTracker{
		entries:      make(map[string]*channelActivity),
		runtimeState: runtimeState,
	}
	for _, name := range channelNames {
		rs, err := runtimeState.Get(ctx, name)
		if err != nil {
			slog.Error("activity tracker: failed to load persisted state", "channel", name, "err", err)
			continue
		}
		if rs.LastMessageAt.IsZero() {
			continue
		}
		t.entries[name] = &channelActivity{
			lastMessageAt:     rs.LastMessageAt,
			lastMessageSource: rs.LastMessageSource,
		}
	}
	return t
}

// MessageReceived records that a message arrived on channelName.
// Called by the router when a message enters the processing pipeline.
func (t *ActivityTracker) MessageReceived(channelName string) {
	t.MessageReceivedFrom(channelName, "")
}

// MessageReceivedFrom records that a message from the given source arrived.
func (t *ActivityTracker) MessageReceivedFrom(channelName string, source MessageSource) {
	t.mu.Lock()
	e := t.entry(channelName)
	e.lastMessageAt = time.Now()
	e.lastMessageSource = source

	// Reset any pending idle timer — the channel just got busier.
	e.cancelIdleTimer()
	t.mu.Unlock()

	t.persistActivity(channelName, e.lastMessageAt, source)
}

// TurnStarted marks channelName as actively processing a turn.
// Called by the router via the agent's OnTurnStart callback.
func (t *ActivityTracker) TurnStarted(channelName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entry(channelName)
	e.processing = true
	e.cancelIdleTimer()
}

// TurnEnded marks channelName as no longer actively processing a turn.
// Called by the router via the agent's OnTurnEnd callback.
func (t *ActivityTracker) TurnEnded(channelName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entry(channelName)
	e.processing = false
	e.scheduleIdleCheck(t, channelName)
}

// IsBusy reports whether channelName is busy using the default idle timeout.
// Returns (busy, known) — see IsBusyWithTimeout.
func (t *ActivityTracker) IsBusy(channelName string) (busy bool, known bool) {
	return t.IsBusyWithTimeout(channelName, DefaultIdleTimeout)
}

// IsBusyWithTimeout reports whether channelName is busy. Returns (busy, known).
// known is false when the tracker has no record of the channel at all — the
// caller must decide how to handle that (e.g. skip cleanup, log a warning).
func (t *ActivityTracker) IsBusyWithTimeout(channelName string, idleTimeout time.Duration) (busy bool, known bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[channelName]
	if !ok {
		return false, false
	}
	return e.processing || time.Since(e.lastMessageAt) < idleTimeout, true
}

// NotifyIdle returns a channel that is closed when channelName transitions
// from busy to idle. If the channel is already idle, the returned channel
// is closed immediately. Each call returns a new channel — callers should
// call this once and select on the result.
func (t *ActivityTracker) NotifyIdle(channelName string) <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch := make(chan struct{})

	e, ok := t.entries[channelName]
	if !ok {
		// Unknown channel — not busy.
		close(ch)
		return ch
	}

	if !e.isBusy() {
		close(ch)
		return ch
	}

	e.idleWaiters = append(e.idleWaiters, ch)

	// If the channel is in its cooldown window (not processing, just waiting
	// for the idle timeout to expire), schedule a timer. TurnEnded only
	// schedules a timer if waiters already exist — but this waiter was just
	// added after TurnEnded ran, so no timer is pending.
	if !e.processing && e.idleTimer == nil {
		e.scheduleIdleCheck(t, channelName)
	}

	return ch
}

// entry returns the channelActivity for name, creating it if absent.
// Caller must hold t.mu.
func (t *ActivityTracker) entry(name string) *channelActivity {
	if e, ok := t.entries[name]; ok {
		return e
	}
	e := &channelActivity{}
	t.entries[name] = e
	return e
}

// persistActivity writes lastMessageAt and source to the RuntimeStateStore.
// Best-effort — errors are logged but don't block the caller.
func (t *ActivityTracker) persistActivity(channelName string, at time.Time, source MessageSource) {
	if t.runtimeState == nil {
		return
	}
	if err := t.runtimeState.Update(context.Background(), channelName, func(s *RuntimeState) {
		s.LastMessageAt = at
		s.LastMessageSource = source
	}); err != nil {
		slog.Error("activity tracker: failed to persist activity", "channel", channelName, "err", err)
	}
}

// ForceLastMessageAt sets the lastMessageAt for a channel to an arbitrary time.
// Test-only — used to simulate aged activity for cleanup testing.
func (t *ActivityTracker) ForceLastMessageAt(channelName string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entry(channelName)
	e.lastMessageAt = at
}

// isBusy checks busy state without locking (caller must hold tracker mutex).
func (e *channelActivity) isBusy() bool {
	return e.processing || time.Since(e.lastMessageAt) < DefaultIdleTimeout
}

// cancelIdleTimer stops any pending idle check timer.
// Caller must hold tracker mutex.
func (e *channelActivity) cancelIdleTimer() {
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
}

// scheduleIdleCheck starts a timer that will signal idle waiters after the
// idle timeout expires (if the channel is still idle at that point).
// Caller must hold tracker mutex.
func (e *channelActivity) scheduleIdleCheck(t *ActivityTracker, channelName string) {
	e.cancelIdleTimer()

	// If there are no waiters, don't bother with a timer.
	if len(e.idleWaiters) == 0 {
		return
	}

	// Calculate how long until the idle timeout expires.
	remaining := DefaultIdleTimeout - time.Since(e.lastMessageAt)
	if remaining <= 0 {
		// Already past idle timeout — signal immediately.
		e.signalWaiters()
		return
	}

	e.idleTimer = time.AfterFunc(remaining, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		entry, ok := t.entries[channelName]
		if !ok {
			return
		}
		if !entry.isBusy() {
			entry.signalWaiters()
		}
	})
}

// signalWaiters closes all idle waiter channels and clears the list.
// Caller must hold tracker mutex.
func (e *channelActivity) signalWaiters() {
	for _, ch := range e.idleWaiters {
		close(ch)
	}
	e.idleWaiters = nil
}
