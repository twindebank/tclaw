package channel

import (
	"testing"
	"time"
)

func TestActivityTracker(t *testing.T) {
	t.Run("NotifyIdle returns closed channel for unknown channel", func(t *testing.T) {
		tracker := NewActivityTracker()
		ch := tracker.NotifyIdle("unknown")
		select {
		case <-ch:
		default:
			t.Fatal("expected closed channel for unknown channel")
		}
	})

	t.Run("NotifyIdle returns closed channel when already idle", func(t *testing.T) {
		tracker := NewActivityTracker()

		// Set lastMessageAt far in the past so the cooldown is expired.
		tracker.mu.Lock()
		e := tracker.entry("test")
		e.lastMessageAt = time.Now().Add(-DefaultIdleTimeout - time.Second)
		tracker.mu.Unlock()

		ch := tracker.NotifyIdle("test")
		select {
		case <-ch:
		default:
			t.Fatal("expected closed channel when already idle")
		}
	})

	t.Run("NotifyIdle fires after processing ends with expired cooldown", func(t *testing.T) {
		tracker := NewActivityTracker()
		tracker.TurnStarted("test")

		ch := tracker.NotifyIdle("test")
		select {
		case <-ch:
			t.Fatal("should not be idle while processing")
		default:
		}

		// Backdate the last message so the cooldown is already expired,
		// then end the turn — waiter should fire immediately.
		tracker.mu.Lock()
		tracker.entries["test"].lastMessageAt = time.Now().Add(-DefaultIdleTimeout - time.Second)
		tracker.mu.Unlock()

		tracker.TurnEnded("test")

		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("expected idle notification after turn ended with expired cooldown")
		}
	})

	t.Run("waiter added after TurnEnded fires when cooldown expires", func(t *testing.T) {
		// This is the critical race condition: TurnEnded runs when there are
		// no waiters (so no timer is scheduled), then a waiter is added later
		// via NotifyIdle. The waiter must still fire when the cooldown expires.
		tracker := NewActivityTracker()

		// Simulate: message received, turn started, turn ended.
		// Set lastMessageAt to almost expired so the timer fires quickly.
		tracker.MessageReceived("email")
		tracker.TurnStarted("email")

		tracker.mu.Lock()
		tracker.entries["email"].lastMessageAt = time.Now().Add(-DefaultIdleTimeout + 50*time.Millisecond)
		tracker.mu.Unlock()

		tracker.TurnEnded("email")

		// At this point, scheduleIdleCheck ran but found no waiters, so no
		// timer was set. Now add a waiter (simulates queue calling NotifyIdle
		// after a notification arrives).
		ch := tracker.NotifyIdle("email")

		// Should fire within ~100ms (50ms remaining + margin).
		select {
		case <-ch:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("waiter added after TurnEnded should fire once cooldown expires")
		}
	})

	t.Run("NotifyIdle schedules timer during cooldown window", func(t *testing.T) {
		// Verify that calling NotifyIdle during the cooldown window (not
		// processing, but within DefaultIdleTimeout of lastMessageAt)
		// schedules a timer to fire the waiter.
		tracker := NewActivityTracker()

		// Set lastMessageAt to 50ms before expiry.
		tracker.mu.Lock()
		e := tracker.entry("email")
		e.lastMessageAt = time.Now().Add(-DefaultIdleTimeout + 50*time.Millisecond)
		tracker.mu.Unlock()

		ch := tracker.NotifyIdle("email")

		// Should fire within ~100ms (50ms remaining + margin).
		select {
		case <-ch:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("NotifyIdle should schedule a timer that fires after cooldown expires")
		}
	})

	t.Run("IsBusy during processing", func(t *testing.T) {
		tracker := NewActivityTracker()
		tracker.TurnStarted("test")
		if !tracker.IsBusy("test") {
			t.Fatal("expected busy during processing")
		}
		tracker.TurnEnded("test")
	})

	t.Run("IsBusy during cooldown", func(t *testing.T) {
		tracker := NewActivityTracker()
		tracker.MessageReceived("test")
		if !tracker.IsBusy("test") {
			t.Fatal("expected busy during cooldown")
		}
	})

	t.Run("not busy for unknown channel", func(t *testing.T) {
		tracker := NewActivityTracker()
		if tracker.IsBusy("unknown") {
			t.Fatal("expected not busy for unknown channel")
		}
	})
}
