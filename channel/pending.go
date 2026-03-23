package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/libraries/store"
)

const pendingStoreKey = "pending_sends"

// PendingMessage is a cross-channel message queued for delivery when the
// target channel becomes free. Persisted in the state store so it survives
// agent and process restarts.
type PendingMessage struct {
	ID          string    `json:"id"`
	FromChannel string    `json:"from_channel"`
	ToChannel   string    `json:"to_channel"`
	Message     string    `json:"message"`
	QueuedAt    time.Time `json:"queued_at"`

	// ExpiresAt is the deliver-anyway deadline. If the target channel is still
	// busy after this time, the message is delivered with a [delayed] prefix.
	ExpiresAt time.Time `json:"expires_at"`
}

// PendingStore provides durable storage for pending cross-channel messages.
// Messages are persisted as a JSON array under a single store key.
type PendingStore struct {
	store store.Store
}

// NewPendingStore creates a PendingStore backed by the given store.
func NewPendingStore(s store.Store) *PendingStore {
	return &PendingStore{store: s}
}

// Add persists a new pending message to the store.
func (s *PendingStore) Add(ctx context.Context, msg PendingMessage) error {
	messages, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("read pending messages: %w", err)
	}

	messages = append(messages, msg)
	return s.save(ctx, messages)
}

// List returns all pending messages from the store.
func (s *PendingStore) List(ctx context.Context) ([]PendingMessage, error) {
	raw, err := s.store.Get(ctx, pendingStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read pending store: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}

	var messages []PendingMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, fmt.Errorf("unmarshal pending messages: %w", err)
	}
	return messages, nil
}

// Remove deletes a pending message by ID. Returns an error if the store
// read/write fails — never silently drops a message.
func (s *PendingStore) Remove(ctx context.Context, id string) error {
	messages, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("read pending messages for removal: %w", err)
	}

	filtered := messages[:0]
	for _, m := range messages {
		if m.ID != id {
			filtered = append(filtered, m)
		}
	}
	return s.save(ctx, filtered)
}

func (s *PendingStore) save(ctx context.Context, messages []PendingMessage) error {
	data, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("marshal pending messages: %w", err)
	}
	if err := s.store.Set(ctx, pendingStoreKey, data); err != nil {
		return fmt.Errorf("write pending store: %w", err)
	}
	return nil
}
