// Package queue provides a persistent priority queue for agent messages.
//
// All message sources (user, schedule, cross-channel, notification) flow
// through one queue. Dequeue rules:
//
//  1. User and resume messages are always processable — dequeued immediately.
//  2. Everything else waits until the target channel is idle (not busy).
//
// The queue persists to disk so messages survive restarts. It uses the
// ActivityTracker's NotifyIdle mechanism for event-driven wake instead of
// polling on a timer.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"tclaw/channel"
	"tclaw/libraries/store"
)

const storeKey = "message_queue"

// ErrInputClosed is returned by Next when the input channel is closed.
var ErrInputClosed = errors.New("input channel closed")

// QueuedMessage is a message waiting for the agent to process.
type QueuedMessage struct {
	ChannelID  channel.ChannelID          `json:"channel_id"`
	Text       string                     `json:"text"`
	SourceInfo *channel.MessageSourceInfo `json:"source_info,omitempty"`
	QueuedAt   time.Time                  `json:"queued_at"`
}

// persistedState is the on-disk format.
type persistedState struct {
	Messages []QueuedMessage `json:"messages"`

	// InterruptedChannel is the channel that was mid-turn when the agent
	// was interrupted. On restart, a resume message is injected.
	InterruptedChannel channel.ChannelID `json:"interrupted_channel,omitempty"`
}

// QueueParams holds dependencies for creating a Queue.
type QueueParams struct {
	Store    store.Store
	Activity *channel.ActivityTracker
	Channels func() map[channel.ChannelID]channel.Channel
}

// Queue is a persistent priority queue for agent messages.
type Queue struct {
	store    store.Store
	activity *channel.ActivityTracker
	channels func() map[channel.ChannelID]channel.Channel

	mu                 sync.Mutex
	messages           []QueuedMessage
	interruptedChannel channel.ChannelID

	// notify is signalled (non-blocking) whenever a message is pushed,
	// so Next() can re-evaluate dequeueability.
	notify chan struct{}
}

// New creates a Queue from the given params.
func New(p QueueParams) *Queue {
	return &Queue{
		store:    p.Store,
		activity: p.Activity,
		channels: p.Channels,
		notify:   make(chan struct{}, 1),
	}
}

// LoadPersisted restores messages and interrupted state from the store.
// Called once on agent startup before the first Next() call.
func (q *Queue) LoadPersisted(ctx context.Context) error {
	raw, err := q.store.Get(ctx, storeKey)
	if err != nil {
		return fmt.Errorf("read queue store: %w", err)
	}
	if len(raw) == 0 {
		return nil
	}

	var state persistedState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("unmarshal queue state: %w", err)
	}

	q.mu.Lock()
	q.messages = state.Messages
	q.interruptedChannel = state.InterruptedChannel
	q.mu.Unlock()

	return nil
}

// Push adds a message to the queue, persists it, and wakes Next().
func (q *Queue) Push(ctx context.Context, msg channel.TaggedMessage) error {
	qm := QueuedMessage{
		ChannelID:  msg.ChannelID,
		Text:       msg.Text,
		SourceInfo: msg.SourceInfo,
		QueuedAt:   time.Now(),
	}

	q.mu.Lock()
	q.messages = append(q.messages, qm)
	q.mu.Unlock()

	if err := q.persist(ctx); err != nil {
		slog.Error("queue: failed to persist after push", "error", err)
		return err
	}

	// Wake Next() non-blocking.
	select {
	case q.notify <- struct{}{}:
	default:
	}

	return nil
}

// Next blocks until a processable message is available and returns it.
// User/resume messages are always processable. Non-user messages are only
// processable when the target channel is idle.
func (q *Queue) Next(ctx context.Context, input <-chan channel.TaggedMessage) (channel.TaggedMessage, error) {
	for {
		// Try to dequeue a processable message.
		if msg, ok := q.tryDequeue(ctx); ok {
			return msg, nil
		}

		// Build the idle notification channel for any queued non-user messages.
		idleCh := q.idleNotifyForQueued()

		select {
		case <-ctx.Done():
			return channel.TaggedMessage{}, ctx.Err()

		case m, ok := <-input:
			if !ok {
				return channel.TaggedMessage{}, ErrInputClosed
			}
			if err := q.Push(ctx, m); err != nil {
				slog.Error("queue: push failed in Next", "error", err)
			}
			// Loop back to try dequeue — the new message might be processable.

		case <-idleCh:
			// A channel became idle — retry dequeue.

		case <-q.notify:
			// A message was pushed externally (by bridge goroutine) — retry.
		}
	}
}

// tryDequeue finds and removes the highest-priority processable message.
// Returns false if nothing is processable right now.
func (q *Queue) tryDequeue(ctx context.Context) (channel.TaggedMessage, bool) {
	q.mu.Lock()
	idx := q.dequeueIndex()
	if idx < 0 {
		q.mu.Unlock()
		return channel.TaggedMessage{}, false
	}

	qm := q.messages[idx]
	q.messages = append(q.messages[:idx], q.messages[idx+1:]...)
	q.mu.Unlock()

	if err := q.persist(ctx); err != nil {
		slog.Error("queue: failed to persist after dequeue", "error", err)
	}

	return channel.TaggedMessage{
		ChannelID:  qm.ChannelID,
		Text:       qm.Text,
		SourceInfo: qm.SourceInfo,
	}, true
}

// dequeueIndex returns the index of the highest-priority processable message,
// or -1 if none is processable. Caller must hold q.mu.
func (q *Queue) dequeueIndex() int {
	// Priority 1: user messages — always processable.
	for i, m := range q.messages {
		if isUserMessage(m) {
			return i
		}
	}

	// Priority 2: resume messages — always processable.
	for i, m := range q.messages {
		if m.SourceInfo != nil && m.SourceInfo.Source == channel.SourceResume {
			return i
		}
	}

	// Priority 3: non-user messages — only if target channel is not busy.
	for i, m := range q.messages {
		channelName := q.resolveChannelName(m.ChannelID)
		if channelName == "" || !q.activity.IsBusy(channelName) {
			return i
		}
	}

	return -1
}

// isUserMessage returns true for messages typed by a human.
func isUserMessage(m QueuedMessage) bool {
	return m.SourceInfo == nil || m.SourceInfo.Source == channel.SourceUser
}

// idleNotifyForQueued returns a channel that fires when any channel with
// queued non-user messages becomes idle. Returns a closed channel if there
// are no non-user messages waiting on busy channels.
func (q *Queue) idleNotifyForQueued() <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Collect unique busy channel names that have queued non-user messages.
	busyChannels := make(map[string]bool)
	for _, m := range q.messages {
		if isUserMessage(m) {
			continue
		}
		name := q.resolveChannelName(m.ChannelID)
		if name != "" && q.activity.IsBusy(name) {
			busyChannels[name] = true
		}
	}

	if len(busyChannels) == 0 {
		// Nothing waiting on busy channels — return a closed channel
		// so the select in Next() doesn't block on this case.
		ch := make(chan struct{})
		close(ch)
		return ch
	}

	// Fan multiple NotifyIdle channels into one.
	merged := make(chan struct{}, 1)
	for name := range busyChannels {
		idle := q.activity.NotifyIdle(name)
		go func() {
			<-idle
			select {
			case merged <- struct{}{}:
			default:
			}
		}()
	}

	return merged
}

// resolveChannelName maps a ChannelID to a channel name.
// Caller must hold q.mu (or accept that channels() is concurrent-safe).
func (q *Queue) resolveChannelName(id channel.ChannelID) string {
	ch, ok := q.channels()[id]
	if !ok {
		return ""
	}
	return ch.Info().Name
}

// SetInterrupted marks the given channel as having been interrupted mid-turn.
func (q *Queue) SetInterrupted(ctx context.Context, chID channel.ChannelID) error {
	q.mu.Lock()
	q.interruptedChannel = chID
	q.mu.Unlock()
	return q.persist(ctx)
}

// ClearInterrupted removes the interrupted channel marker.
func (q *Queue) ClearInterrupted(ctx context.Context) error {
	q.mu.Lock()
	q.interruptedChannel = ""
	q.mu.Unlock()
	return q.persist(ctx)
}

// InterruptedChannel returns the channel that was interrupted, if any.
func (q *Queue) InterruptedChannel() channel.ChannelID {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.interruptedChannel
}

// Len returns the current queue depth.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages)
}

func (q *Queue) persist(ctx context.Context) error {
	q.mu.Lock()
	state := persistedState{
		Messages:           q.messages,
		InterruptedChannel: q.interruptedChannel,
	}
	q.mu.Unlock()

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal queue state: %w", err)
	}
	if err := q.store.Set(ctx, storeKey, data); err != nil {
		return fmt.Errorf("write queue store: %w", err)
	}
	return nil
}
