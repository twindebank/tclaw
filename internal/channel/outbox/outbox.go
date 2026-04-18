// Package outbox provides a persistent outbound message queue for channel delivery.
//
// The Outbox accepts Send/Edit/Done operations and delivers them asynchronously
// via per-channel FIFO queues with retry. Callers never block on network I/O —
// enqueue is instant, and delivery happens in the background. Operations persist
// to disk so undelivered messages survive process restarts.
//
// This is the outbound counterpart to the inbound queue (internal/queue):
//
//	Inbound:  Channels → FanIn → *Queue → agent processes
//	Outbound: agent → *Outbox → per-channel delivery → Channels
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/id"
	"tclaw/internal/libraries/store"
)

// OpKind identifies the type of queued operation.
type OpKind int

const (
	OpSend OpKind = iota
	OpEdit
	OpDone
)

// Op is a single queued outbound operation.
type Op struct {
	Kind      OpKind            `json:"kind"`
	ChannelID channel.ChannelID `json:"channel_id"`

	// ProxyID is assigned by Send and returned to the caller immediately.
	// The delivery goroutine maps it to the real MessageID after delivery.
	ProxyID channel.MessageID `json:"proxy_id,omitempty"`

	// TargetID is the proxy MessageID of the message to edit (Edit ops only).
	TargetID channel.MessageID `json:"target_id,omitempty"`

	Text string `json:"text"`

	// Notify carries the SendOpts.Notify hint through the queue so the
	// allowlist decision survives persistence and restart. Defaults to false
	// (silent) on ops persisted before this field existed.
	Notify bool `json:"notify,omitempty"`

	// Seq is a monotonic counter used for edit coalescing — when multiple
	// Edits target the same message, only the highest-Seq one is delivered.
	Seq uint64 `json:"seq"`
}

// persistedState is the on-disk format for a single channel's queue.
type persistedState struct {
	Ops      []Op                                    `json:"ops"`
	ProxyMap map[channel.MessageID]channel.MessageID `json:"proxy_map"`
	Seq      uint64                                  `json:"seq"`
}

// channelQueue holds the per-channel operation queue and delivery state.
type channelQueue struct {
	ops      []Op
	proxyMap map[channel.MessageID]channel.MessageID

	// wake signals the delivery goroutine that new ops are available.
	wake chan struct{}

	// done is closed when the delivery goroutine exits.
	done chan struct{}

	// drainWaiters are closed when the queue is empty AND no delivery is
	// in-flight. Flush callers add a waiter then select on it.
	drainWaiters []chan struct{}

	// delivering is true while the delivery goroutine is executing an op
	// against the real channel. Flush must wait for this to be false.
	delivering bool

	// dirty tracks whether the queue has changed since the last persist.
	dirty bool
}

// Params holds dependencies for creating an Outbox.
type Params struct {
	Store    store.Store
	Channels func() map[channel.ChannelID]channel.Channel
}

// Outbox manages outbound message delivery for all channels.
type Outbox struct {
	store    store.Store
	channels func() map[channel.ChannelID]channel.Channel

	mu     sync.Mutex
	queues map[channel.ChannelID]*channelQueue
	seq    uint64

	ctx    context.Context
	cancel context.CancelFunc
}

// New creates an Outbox. Call Start to begin delivery.
func New(p Params) *Outbox {
	return &Outbox{
		store:    p.Store,
		channels: p.Channels,
		queues:   make(map[channel.ChannelID]*channelQueue),
	}
}

// Start begins background delivery goroutines and loads persisted state.
// Safe to call multiple times (e.g. on each agent iteration) — stops
// existing delivery goroutines before starting new ones.
func (o *Outbox) Start(ctx context.Context) {
	// Stop any delivery goroutines from a previous Start call. Without
	// this, the old goroutines' defer close(cq.done) races with new
	// goroutines using the same channelQueue, causing a double-close panic.
	if o.cancel != nil {
		o.cancel()
		o.mu.Lock()
		var dones []chan struct{}
		for _, cq := range o.queues {
			dones = append(dones, cq.done)
		}
		o.mu.Unlock()
		for _, done := range dones {
			<-done
		}
	}

	o.ctx, o.cancel = context.WithCancel(ctx)

	if err := o.loadPersisted(); err != nil {
		slog.Error("outbox: failed to load persisted state", "error", err)
	}

	// Reset done channels and start delivery goroutines.
	o.mu.Lock()
	for _, cq := range o.queues {
		cq.done = make(chan struct{})
	}
	for chID := range o.queues {
		o.startDelivery(chID)
	}
	o.mu.Unlock()
}

// Send enqueues a message for delivery. Returns a proxy MessageID immediately.
// Returns an error only if the operation fails to enqueue (e.g. store write failure).
func (o *Outbox) Send(ctx context.Context, chID channel.ChannelID, text string, opts channel.SendOpts) (channel.MessageID, error) {
	proxyID := channel.MessageID(id.Generate("outbox"))

	op := Op{
		Kind:      OpSend,
		ChannelID: chID,
		ProxyID:   proxyID,
		Text:      text,
		Notify:    opts.Notify,
	}

	if err := o.enqueue(chID, op, true); err != nil {
		return "", fmt.Errorf("outbox send: %w", err)
	}

	return proxyID, nil
}

// Edit enqueues an update to a previously sent message. The proxyID must be a
// MessageID returned by a prior Send call — the delivery goroutine resolves it
// to the real transport ID before delivering.
func (o *Outbox) Edit(ctx context.Context, chID channel.ChannelID, proxyID channel.MessageID, text string) error {
	op := Op{
		Kind:      OpEdit,
		ChannelID: chID,
		TargetID:  proxyID,
		Text:      text,
	}

	// Edits are high-frequency during streaming — persist periodically, not on every enqueue.
	if err := o.enqueue(chID, op, false); err != nil {
		return fmt.Errorf("outbox edit: %w", err)
	}

	return nil
}

// Done enqueues a turn-end signal. Delivered after all preceding operations complete.
func (o *Outbox) Done(ctx context.Context, chID channel.ChannelID) error {
	op := Op{
		Kind:      OpDone,
		ChannelID: chID,
	}

	if err := o.enqueue(chID, op, true); err != nil {
		return fmt.Errorf("outbox done: %w", err)
	}

	return nil
}

// Flush blocks until all pending operations across all channels are delivered.
func (o *Outbox) Flush(ctx context.Context) error {
	o.mu.Lock()
	var waiters []chan struct{}
	for _, cq := range o.queues {
		// Wait if there are queued ops OR a delivery is in-flight (the op
		// was dequeued but hasn't been delivered yet, e.g. retrying).
		if len(cq.ops) == 0 && !cq.delivering {
			continue
		}
		ch := make(chan struct{})
		cq.drainWaiters = append(cq.drainWaiters, ch)
		waiters = append(waiters, ch)

		select {
		case cq.wake <- struct{}{}:
		default:
		}
	}
	o.mu.Unlock()

	for _, w := range waiters {
		select {
		case <-w:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// Stop cancels delivery and persists remaining state.
func (o *Outbox) Stop() {
	if o.cancel != nil {
		o.cancel()
	}

	// Wait for all delivery goroutines to exit.
	o.mu.Lock()
	var dones []chan struct{}
	for _, cq := range o.queues {
		dones = append(dones, cq.done)
	}
	o.mu.Unlock()

	for _, done := range dones {
		<-done
	}
}

// enqueue adds an operation to the channel's queue and optionally persists.
func (o *Outbox) enqueue(chID channel.ChannelID, op Op, persist bool) error {
	o.mu.Lock()
	o.seq++
	op.Seq = o.seq

	cq, ok := o.queues[chID]
	if !ok {
		cq = &channelQueue{
			proxyMap: make(map[channel.MessageID]channel.MessageID),
			wake:     make(chan struct{}, 1),
			done:     make(chan struct{}),
		}
		o.queues[chID] = cq
		o.startDelivery(chID)
	}
	cq.ops = append(cq.ops, op)
	cq.dirty = true
	o.mu.Unlock()

	if persist {
		if err := o.persistChannel(chID); err != nil {
			return fmt.Errorf("persist: %w", err)
		}
	}

	// Wake the delivery goroutine.
	select {
	case cq.wake <- struct{}{}:
	default:
	}

	return nil
}

// startDelivery launches the delivery goroutine for a channel.
// Caller must hold o.mu.
func (o *Outbox) startDelivery(chID channel.ChannelID) {
	cq := o.queues[chID]
	go o.deliverLoop(chID, cq)
}

// deliverLoop processes operations for a single channel in FIFO order.
func (o *Outbox) deliverLoop(chID channel.ChannelID, cq *channelQueue) {
	defer close(cq.done)

	flushTicker := time.NewTicker(500 * time.Millisecond)
	defer flushTicker.Stop()

	for {
		// Check for shutdown before dequeuing to avoid infinite retry loops
		// when ctx is cancelled (deliver re-enqueues, dequeue pops it, repeat).
		if o.ctx.Err() != nil {
			o.mu.Lock()
			o.persistChannelLocked(chID, cq)
			o.mu.Unlock()
			return
		}

		op, ok := o.dequeue(cq)
		if ok {
			o.mu.Lock()
			cq.delivering = true
			o.mu.Unlock()

			o.deliver(chID, cq, op)

			o.mu.Lock()
			cq.delivering = false
			o.mu.Unlock()

			o.signalDrainIfEmpty(cq)
			continue
		}

		// Queue is empty and nothing in-flight — signal Flush callers.
		o.signalDrainIfEmpty(cq)

		select {
		case <-o.ctx.Done():
			o.mu.Lock()
			o.persistChannelLocked(chID, cq)
			o.mu.Unlock()
			return
		case <-cq.wake:
		case <-flushTicker.C:
			o.maybePersist(chID, cq)
		}
	}
}

// dequeue removes and returns the next operation, applying edit coalescing.
// Returns false if the queue is empty.
func (o *Outbox) dequeue(cq *channelQueue) (Op, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	for len(cq.ops) > 0 {
		op := cq.ops[0]
		cq.ops = cq.ops[1:]
		cq.dirty = true

		// Edit coalescing: if a later Edit for the same target exists in the
		// remaining queue, skip this one — only the final state matters.
		if op.Kind == OpEdit {
			superseded := false
			for _, later := range cq.ops {
				if later.Kind == OpEdit && later.TargetID == op.TargetID {
					superseded = true
					break
				}
			}
			if superseded {
				continue
			}
		}

		return op, true
	}

	return Op{}, false
}

// deliver executes a single operation against the real channel with retry.
func (o *Outbox) deliver(chID channel.ChannelID, cq *channelQueue, op Op) {
	ch := o.resolveChannel(chID)
	if ch == nil {
		slog.Warn("outbox: channel not found, discarding operation", "channel", chID, "kind", op.Kind)
		return
	}

	delay := time.Second
	const maxDelay = 30 * time.Second
	const maxRetries = 50

	for attempt := 0; attempt <= maxRetries; attempt++ {
		var err error

		switch op.Kind {
		case OpSend:
			var realID channel.MessageID
			realID, err = ch.Send(o.ctx, op.Text, channel.SendOpts{Notify: op.Notify})
			if err == nil {
				o.mu.Lock()
				cq.proxyMap[op.ProxyID] = realID
				cq.dirty = true
				o.mu.Unlock()
			}

		case OpEdit:
			realID := o.resolveProxyID(cq, op.TargetID)
			if realID == "" {
				// Proxy not resolved — the Send was lost (e.g. channel disappeared
				// between Send and Edit). Skip rather than block the queue.
				slog.Warn("outbox: unresolved proxy ID, skipping edit",
					"channel", chID, "proxy_id", op.TargetID)
				return
			}
			err = ch.Edit(o.ctx, realID, op.Text)

		case OpDone:
			err = ch.Done(o.ctx)
		}

		if err == nil {
			return
		}

		if o.ctx.Err() != nil {
			// Shutting down — re-enqueue for persistence and exit.
			o.mu.Lock()
			cq.ops = append([]Op{op}, cq.ops...)
			cq.dirty = true
			o.mu.Unlock()
			return
		}

		if isPermanentError(err) {
			slog.Error("outbox: permanent delivery failure, discarding",
				"channel", chID, "kind", op.Kind, "error", err)
			return
		}

		if isSkippableError(err) {
			return
		}

		slog.Warn("outbox: transient delivery failure, retrying",
			"channel", chID, "kind", op.Kind, "attempt", attempt+1, "delay", delay, "error", err)

		select {
		case <-time.After(delay):
		case <-o.ctx.Done():
			// Shutting down — re-enqueue for persistence.
			o.mu.Lock()
			cq.ops = append([]Op{op}, cq.ops...)
			cq.dirty = true
			o.mu.Unlock()
			return
		}

		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}

	slog.Error("outbox: max retries exceeded, discarding",
		"channel", chID, "kind", op.Kind, "retries", maxRetries)
}

// signalDrainIfEmpty closes drain waiters when the queue is empty and no
// delivery is in-flight.
func (o *Outbox) signalDrainIfEmpty(cq *channelQueue) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(cq.ops) == 0 && !cq.delivering && len(cq.drainWaiters) > 0 {
		for _, w := range cq.drainWaiters {
			close(w)
		}
		cq.drainWaiters = nil
	}
}

func (o *Outbox) resolveChannel(chID channel.ChannelID) channel.Channel {
	channels := o.channels()
	return channels[chID]
}

func (o *Outbox) resolveProxyID(cq *channelQueue, proxyID channel.MessageID) channel.MessageID {
	o.mu.Lock()
	defer o.mu.Unlock()
	return cq.proxyMap[proxyID]
}

// isPermanentError returns true for errors that will never succeed on retry.
func isPermanentError(err error) bool {
	msg := err.Error()
	// Telegram bot blocked by user or chat deleted.
	if strings.Contains(msg, "Forbidden") {
		return true
	}
	if strings.Contains(msg, "chat not found") {
		return true
	}
	if strings.Contains(msg, "bot was blocked") {
		return true
	}
	// Malformed HTML/entities — retrying won't fix the markup.
	if strings.Contains(msg, "can't parse entities") {
		return true
	}
	// Invalid UTF-8 encoding in the message text — Telegram will always reject
	// this regardless of how many times we retry. The transport layer sanitizes
	// UTF-8 before sending, so reaching here means the sanitization was bypassed.
	if strings.Contains(msg, "must be encoded in UTF-8") {
		return true
	}
	return false
}

// isSkippableError returns true for errors that indicate the operation is
// unnecessary (not a failure).
func isSkippableError(err error) bool {
	return strings.Contains(err.Error(), "message is not modified")
}

// persistChannel writes a channel's queue state to the store.
func (o *Outbox) persistChannel(chID channel.ChannelID) error {
	o.mu.Lock()
	cq, ok := o.queues[chID]
	if !ok {
		o.mu.Unlock()
		return nil
	}

	// Snapshot under lock to avoid racing with the delivery goroutine.
	ops := make([]Op, len(cq.ops))
	copy(ops, cq.ops)
	proxyMap := make(map[channel.MessageID]channel.MessageID, len(cq.proxyMap))
	for k, v := range cq.proxyMap {
		proxyMap[k] = v
	}
	seq := o.seq
	cq.dirty = false
	o.mu.Unlock()

	state := persistedState{
		Ops:      ops,
		ProxyMap: proxyMap,
		Seq:      seq,
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return o.store.Set(context.Background(), storeKey(chID), data)
}

// persistChannelLocked persists without acquiring o.mu (caller must hold it or
// be in a context where racing is acceptable, like shutdown).
func (o *Outbox) persistChannelLocked(chID channel.ChannelID, cq *channelQueue) {
	state := persistedState{
		Ops:      cq.ops,
		ProxyMap: cq.proxyMap,
		Seq:      o.seq,
	}

	data, err := json.Marshal(state)
	if err != nil {
		slog.Error("outbox: failed to marshal state on shutdown", "channel", chID, "error", err)
		return
	}
	if err := o.store.Set(context.Background(), storeKey(chID), data); err != nil {
		slog.Error("outbox: failed to persist state on shutdown", "channel", chID, "error", err)
	}
}

// maybePersist writes dirty queue state to disk.
func (o *Outbox) maybePersist(chID channel.ChannelID, cq *channelQueue) {
	o.mu.Lock()
	if !cq.dirty {
		o.mu.Unlock()
		return
	}
	o.mu.Unlock()

	if err := o.persistChannel(chID); err != nil {
		slog.Error("outbox: periodic persist failed", "channel", chID, "error", err)
	}
}

// loadPersisted restores queue state from the store for all channels.
func (o *Outbox) loadPersisted() error {
	channels := o.channels()

	o.mu.Lock()
	defer o.mu.Unlock()

	for chID := range channels {
		raw, err := o.store.Get(context.Background(), storeKey(chID))
		if err != nil {
			slog.Error("outbox: failed to load persisted state", "channel", chID, "error", err)
			continue
		}
		if len(raw) == 0 {
			continue
		}

		var state persistedState
		if err := json.Unmarshal(raw, &state); err != nil {
			slog.Error("outbox: failed to unmarshal persisted state", "channel", chID, "error", err)
			continue
		}

		if len(state.Ops) == 0 {
			continue
		}

		slog.Info("outbox: loaded persisted ops", "channel", chID, "count", len(state.Ops))

		cq := &channelQueue{
			ops:      state.Ops,
			proxyMap: state.ProxyMap,
			wake:     make(chan struct{}, 1),
			done:     make(chan struct{}),
		}
		if cq.proxyMap == nil {
			cq.proxyMap = make(map[channel.MessageID]channel.MessageID)
		}
		o.queues[chID] = cq

		if state.Seq > o.seq {
			o.seq = state.Seq
		}
	}

	return nil
}

func storeKey(chID channel.ChannelID) string {
	return "outbox/" + string(chID)
}
