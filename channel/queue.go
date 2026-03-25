package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/libraries/store"
)

const queueStoreKey = "message_queue"

// QueuedMessage is a message waiting for the agent to process. Persisted in
// the state store so queued messages survive agent and process restarts.
type QueuedMessage struct {
	ChannelID  ChannelID          `json:"channel_id"`
	Text       string             `json:"text"`
	SourceInfo *MessageSourceInfo `json:"source_info,omitempty"`
	QueuedAt   time.Time          `json:"queued_at"`
}

// QueueState holds the full persistent queue state: pending messages and
// an optional marker for the channel that was interrupted mid-turn.
type QueueState struct {
	Messages []QueuedMessage `json:"messages"`

	// InterruptedChannel is the channel that was actively processing a turn
	// when the agent was interrupted. On restart, a resume message is injected
	// for this channel so the agent continues where it left off.
	InterruptedChannel ChannelID `json:"interrupted_channel,omitempty"`
}

// QueueStore provides durable storage for the agent's message queue.
// The entire queue state is persisted as a single JSON object under one
// store key, following the same pattern as PendingStore.
type QueueStore struct {
	store store.Store
}

// NewQueueStore creates a QueueStore backed by the given store.
func NewQueueStore(s store.Store) *QueueStore {
	return &QueueStore{store: s}
}

// Load returns the full queue state from the store.
// Returns a zero-value QueueState if the store is empty.
func (s *QueueStore) Load(ctx context.Context) (QueueState, error) {
	raw, err := s.store.Get(ctx, queueStoreKey)
	if err != nil {
		return QueueState{}, fmt.Errorf("read queue store: %w", err)
	}
	if len(raw) == 0 {
		return QueueState{}, nil
	}

	var state QueueState
	if err := json.Unmarshal(raw, &state); err != nil {
		return QueueState{}, fmt.Errorf("unmarshal queue state: %w", err)
	}
	return state, nil
}

// Save persists the given messages to the store, preserving the current
// interrupted channel marker.
func (s *QueueStore) Save(ctx context.Context, messages []QueuedMessage) error {
	state, err := s.Load(ctx)
	if err != nil {
		return err
	}
	state.Messages = messages
	return s.save(ctx, state)
}

// SetInterrupted marks the given channel as having been interrupted mid-turn.
// Called when a turn starts; cleared by ClearInterrupted on normal completion.
func (s *QueueStore) SetInterrupted(ctx context.Context, chID ChannelID) error {
	state, err := s.Load(ctx)
	if err != nil {
		return err
	}
	state.InterruptedChannel = chID
	return s.save(ctx, state)
}

// ClearInterrupted removes the interrupted channel marker, indicating the
// turn completed normally.
func (s *QueueStore) ClearInterrupted(ctx context.Context) error {
	state, err := s.Load(ctx)
	if err != nil {
		return err
	}
	state.InterruptedChannel = ""
	return s.save(ctx, state)
}

func (s *QueueStore) save(ctx context.Context, state QueueState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal queue state: %w", err)
	}
	if err := s.store.Set(ctx, queueStoreKey, data); err != nil {
		return fmt.Errorf("write queue store: %w", err)
	}
	return nil
}
