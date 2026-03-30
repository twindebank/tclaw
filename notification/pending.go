package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/libraries/id"
	"tclaw/libraries/store"
)

const pendingStoreKey = "notification_pending"

// PendingNotification is a notification waiting for a free channel.
// Persisted so it survives restarts.
type PendingNotification struct {
	ID             string         `json:"id"`
	SubscriptionID SubscriptionID `json:"subscription_id"`
	ChannelName    string         `json:"channel_name"`
	Text           string         `json:"text"`
	QueuedAt       time.Time      `json:"queued_at"`

	// Scope is stored so the drain loop can handle one-shot cleanup.
	Scope Scope `json:"scope"`

	// Label is carried for source info attribution on delivery.
	Label string `json:"label"`
}

// PendingStore provides durable storage for notifications waiting for a
// free channel. Same JSON-array-under-one-key pattern as channel.PendingStore.
type PendingStore struct {
	store store.Store
}

// NewPendingStore creates a PendingStore backed by the given store.
func NewPendingStore(s store.Store) *PendingStore {
	return &PendingStore{store: s}
}

// Add persists a pending notification.
func (s *PendingStore) Add(ctx context.Context, pn PendingNotification) error {
	items, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("read pending notifications: %w", err)
	}

	items = append(items, pn)
	return s.save(ctx, items)
}

// List returns all pending notifications.
func (s *PendingStore) List(ctx context.Context) ([]PendingNotification, error) {
	raw, err := s.store.Get(ctx, pendingStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read pending notification store: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}

	var items []PendingNotification
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("unmarshal pending notifications: %w", err)
	}
	return items, nil
}

// Remove deletes a pending notification by ID.
func (s *PendingStore) Remove(ctx context.Context, pendingID string) error {
	items, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("read pending notifications for removal: %w", err)
	}

	filtered := items[:0]
	for _, item := range items {
		if item.ID != pendingID {
			filtered = append(filtered, item)
		}
	}

	return s.save(ctx, filtered)
}

// GeneratePendingID creates a unique ID for a pending notification.
func GeneratePendingID() string {
	return id.Generate("pendnotif")
}

func (s *PendingStore) save(ctx context.Context, items []PendingNotification) error {
	data, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("marshal pending notifications: %w", err)
	}
	if err := s.store.Set(ctx, pendingStoreKey, data); err != nil {
		return fmt.Errorf("save pending notifications: %w", err)
	}
	return nil
}
