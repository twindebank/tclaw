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
	"strings"
	"sync"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
)

const storeKey = "message_queue"

// maxBatchWindow caps how long a coalesced batch can keep growing before it's
// flushed, so a steady trickle of messages can't defer processing indefinitely
// via the reset-on-arrival debounce window.
const maxBatchWindow = 5 * time.Second

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

// WaitingInfo describes a message that is waiting for a busy channel.
type WaitingInfo struct {
	// ChannelID is the target channel the message will be delivered to.
	ChannelID channel.ChannelID
	// BusyChannelName is the human-readable name of the channel that is busy.
	BusyChannelName string
	// QueuedAt is when the message was first queued.
	QueuedAt time.Time
}

// QueueParams holds dependencies for creating a Queue.
type QueueParams struct {
	Store    store.Store
	Activity *channel.ActivityTracker
	Channels func() map[channel.ChannelID]channel.Channel

	// OnWaiting is called when a queued message starts waiting for a busy
	// channel. Used to send user-visible feedback. May be nil.
	OnWaiting func(WaitingInfo)

	// DebounceWindow coalesces same-channel user messages that arrive within this
	// rolling window into a single dequeued message (e.g. a photo album delivered
	// as separate messages). The window resets each time a sibling arrives, bounded
	// by maxBatchWindow. 0 disables debouncing.
	DebounceWindow time.Duration

	// IsControlMessage reports whether a message is a builtin command (stop, login,
	// auth, compact, fresh-session) that must be processed on its own turn and never
	// batched into a coalesced turn. May be nil (nothing is treated as control).
	IsControlMessage func(channel.TaggedMessage) bool
}

// Queue is a persistent priority queue for agent messages.
type Queue struct {
	store    store.Store
	activity *channel.ActivityTracker
	channels func() map[channel.ChannelID]channel.Channel

	debounceWindow   time.Duration
	isControlMessage func(channel.TaggedMessage) bool

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
		store:            p.Store,
		activity:         p.Activity,
		channels:         p.Channels,
		debounceWindow:   p.DebounceWindow,
		isControlMessage: p.IsControlMessage,
		notify:           make(chan struct{}, 1),
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
		// When debouncing is on and the highest-priority processable message is a
		// coalescable user message, hold it briefly so sibling messages (e.g. the
		// rest of a photo album) can land and be merged into a single turn. Control
		// commands and non-user messages fall through to the immediate path below.
		if q.debounceWindow > 0 {
			if chID, ok := q.peekBatchableChannel(); ok {
				return q.debounceAndCoalesce(ctx, chID, input)
			}
		}

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
		if channelName == "" {
			return i
		}
		busy, _ := q.activity.IsBusy(channelName)
		if !busy {
			return i
		}
	}

	return -1
}

// isUserMessage returns true for messages typed by a human.
func isUserMessage(m QueuedMessage) bool {
	return m.SourceInfo == nil || m.SourceInfo.Source == channel.SourceUser
}

// isBatchable reports whether a queued message can be coalesced with its
// same-channel siblings: it must be a human-typed message and not a builtin
// control command (which must run on its own turn). Caller must hold q.mu.
func (q *Queue) isBatchable(m QueuedMessage) bool {
	if !isUserMessage(m) {
		return false
	}
	if q.isControlMessage == nil {
		return true
	}
	return !q.isControlMessage(channel.TaggedMessage{
		ChannelID:  m.ChannelID,
		Text:       m.Text,
		SourceInfo: m.SourceInfo,
	})
}

// peekBatchableChannel reports the ChannelID of the highest-priority processable
// message when that message is batchable, so Next can debounce its channel. If
// the top message is instead a control command (or nothing is processable), it
// returns false so Next takes the immediate path and handles it alone.
func (q *Queue) peekBatchableChannel() (channel.ChannelID, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	idx := q.dequeueIndex()
	if idx < 0 {
		return "", false
	}
	m := q.messages[idx]
	if !q.isBatchable(m) {
		return "", false
	}
	return m.ChannelID, true
}

// debounceAndCoalesce holds a ready batchable message for debounceWindow, letting
// same-channel siblings drain into the queue, then coalesces every queued
// batchable message for chID into one message. The window resets each time a new
// message arrives — via the input channel or an external Push (q.notify) — so a
// trickling photo album stays together, bounded by maxBatchWindow so a steady
// stream can't defer processing forever.
func (q *Queue) debounceAndCoalesce(ctx context.Context, chID channel.ChannelID, input <-chan channel.TaggedMessage) (channel.TaggedMessage, error) {
	timer := time.NewTimer(q.debounceWindow)
	defer timer.Stop()
	hardCap := time.After(maxBatchWindow)

	for {
		select {
		case <-ctx.Done():
			// Pushed siblings are already persisted, so nothing is lost by bailing.
			return channel.TaggedMessage{}, ctx.Err()

		case m, ok := <-input:
			if !ok {
				return channel.TaggedMessage{}, ErrInputClosed
			}
			if err := q.Push(ctx, m); err != nil {
				slog.Error("queue: push failed during debounce", "error", err)
			}
			// Reset-on-arrival: a fresh sibling extends the window.
			resetTimer(timer, q.debounceWindow)

		case <-q.notify:
			// A message was pushed externally (bridge goroutine) — extend the window.
			resetTimer(timer, q.debounceWindow)

		case <-timer.C:
			return q.coalesceBatch(ctx, chID)

		case <-hardCap:
			return q.coalesceBatch(ctx, chID)
		}
	}
}

// coalesceBatch removes every queued batchable message for chID in FIFO order and
// returns them as a single message — texts joined by a blank line, carrying the
// first message's SourceInfo. Control, non-user, and other-channel messages are
// left in place so priority semantics hold (e.g. a stop queued after an album is
// handled on the next Next call, cancelling the batch turn).
func (q *Queue) coalesceBatch(ctx context.Context, chID channel.ChannelID) (channel.TaggedMessage, error) {
	q.mu.Lock()

	var texts []string
	var sourceInfo *channel.MessageSourceInfo
	kept := make([]QueuedMessage, 0, len(q.messages))
	for _, m := range q.messages {
		if m.ChannelID == chID && q.isBatchable(m) {
			if len(texts) == 0 {
				sourceInfo = m.SourceInfo
			}
			texts = append(texts, m.Text)
			continue
		}
		kept = append(kept, m)
	}

	if len(texts) == 0 {
		// The peeked message vanished before we could collect it. With a single
		// Next consumer this shouldn't happen; surface it rather than returning an
		// empty message the caller would treat as a real turn.
		q.mu.Unlock()
		return channel.TaggedMessage{}, fmt.Errorf("coalesce batch for channel %q: no batchable messages", chID)
	}

	q.messages = kept
	q.mu.Unlock()

	if err := q.persist(ctx); err != nil {
		slog.Error("queue: failed to persist after coalesce", "error", err)
	}

	return channel.TaggedMessage{
		ChannelID:  chID,
		Text:       strings.Join(texts, "\n\n"),
		SourceInfo: sourceInfo,
	}, nil
}

// resetTimer resets t to fire after d, draining a pending expiry first so the
// next receive on t.C reflects the new deadline rather than a stale one.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
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
		busy, _ := q.activity.IsBusy(name)
		if name != "" && busy {
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
