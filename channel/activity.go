package channel

import (
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
type ActivityTracker struct {
	mu      sync.Mutex
	entries map[string]*channelActivity
}

type channelActivity struct {
	// processing is true while an agent turn is actively running for this channel.
	processing bool

	// lastMessageAt is the time the most recent inbound message was received.
	lastMessageAt time.Time

	// idleWaiters are closed when the channel transitions from busy to idle.
	// Consumers call NotifyIdle() to get a waiter channel, then select on it.
	idleWaiters []chan struct{}

	// idleTimer fires after the idle timeout to signal waiters.
	// Nil when no timer is pending.
	idleTimer *time.Timer
}

// NewActivityTracker returns an initialised ActivityTracker.
func NewActivityTracker() *ActivityTracker {
	return &ActivityTracker{
		entries: make(map[string]*channelActivity),
	}
}

// MessageReceived records that a message arrived on channelName.
// Called by the router when a message enters the processing pipeline.
func (t *ActivityTracker) MessageReceived(channelName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entry(channelName)
	e.lastMessageAt = time.Now()

	// Reset any pending idle timer — the channel just got busier.
	e.cancelIdleTimer()
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

// IsBusy returns true if channelName is actively processing a turn or
// received a message within the default idle timeout window.
func (t *ActivityTracker) IsBusy(channelName string) bool {
	return t.IsBusyWithTimeout(channelName, DefaultIdleTimeout)
}

// IsBusyWithTimeout returns true if channelName is actively processing a turn
// or received a message within the given idle timeout window.
func (t *ActivityTracker) IsBusyWithTimeout(channelName string, idleTimeout time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[channelName]
	if !ok {
		return false
	}
	return e.processing || time.Since(e.lastMessageAt) < idleTimeout
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
