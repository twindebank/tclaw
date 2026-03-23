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
	t.entry(channelName).lastMessageAt = time.Now()
}

// TurnStarted marks channelName as actively processing a turn.
// Called by the router via the agent's OnTurnStart callback.
func (t *ActivityTracker) TurnStarted(channelName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entry(channelName).processing = true
}

// TurnEnded marks channelName as no longer actively processing a turn.
// Called by the router via the agent's OnTurnEnd callback.
func (t *ActivityTracker) TurnEnded(channelName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entry(channelName).processing = false
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
