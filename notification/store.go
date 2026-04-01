package notification

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/libraries/store"
)

const subscriptionsStoreKey = "notification_subscriptions"

// Store manages CRUD for notification subscriptions, persisted as a JSON
// array under a single store key.
type Store struct {
	store store.Store
}

// NewStore creates a subscription store backed by the given store.
func NewStore(s store.Store) *Store {
	return &Store{store: s}
}

// List returns all subscriptions.
func (s *Store) List(ctx context.Context) ([]Subscription, error) {
	data, err := s.store.Get(ctx, subscriptionsStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read subscriptions: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	var subs []Subscription
	if err := json.Unmarshal(data, &subs); err != nil {
		return nil, fmt.Errorf("parse subscriptions: %w", err)
	}
	return subs, nil
}

// Get returns a single subscription by ID, or nil if not found.
func (s *Store) Get(ctx context.Context, id SubscriptionID) (*Subscription, error) {
	subs, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, sub := range subs {
		if sub.ID == id {
			return &sub, nil
		}
	}
	return nil, nil
}

// FindExisting returns a subscription matching the given identity fields, or nil if none exists.
// Used by Manager.Subscribe to prevent duplicate subscriptions.
func (s *Store) FindExisting(ctx context.Context, packageName, typeName, channelName string, scope Scope, credentialSetID string) (*Subscription, error) {
	subs, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, sub := range subs {
		if sub.PackageName == packageName &&
			sub.TypeName == typeName &&
			sub.ChannelName == channelName &&
			sub.Scope == scope &&
			sub.CredentialSetID == credentialSetID {
			return &sub, nil
		}
	}
	return nil, nil
}

// Add persists a new subscription. Returns an error if one with the same ID exists.
func (s *Store) Add(ctx context.Context, sub Subscription) error {
	subs, err := s.List(ctx)
	if err != nil {
		return err
	}

	for _, existing := range subs {
		if existing.ID == sub.ID {
			return fmt.Errorf("subscription %q already exists", sub.ID)
		}
	}

	subs = append(subs, sub)
	return s.save(ctx, subs)
}

// Remove deletes a subscription by ID.
func (s *Store) Remove(ctx context.Context, id SubscriptionID) error {
	subs, err := s.List(ctx)
	if err != nil {
		return err
	}

	found := false
	var remaining []Subscription
	for _, sub := range subs {
		if sub.ID == id {
			found = true
			continue
		}
		remaining = append(remaining, sub)
	}
	if !found {
		return fmt.Errorf("subscription %q not found", id)
	}

	return s.save(ctx, remaining)
}

// RemoveByCredentialSet removes all subscriptions tied to a credential set.
// Returns the removed subscriptions so the caller can cancel their watchers.
func (s *Store) RemoveByCredentialSet(ctx context.Context, credentialSetID string) ([]Subscription, error) {
	subs, err := s.List(ctx)
	if err != nil {
		return nil, err
	}

	var remaining []Subscription
	var removed []Subscription
	for _, sub := range subs {
		if sub.CredentialSetID == credentialSetID {
			removed = append(removed, sub)
			continue
		}
		remaining = append(remaining, sub)
	}

	if len(removed) == 0 {
		return nil, nil
	}

	if err := s.save(ctx, remaining); err != nil {
		return nil, err
	}
	return removed, nil
}

func (s *Store) save(ctx context.Context, subs []Subscription) error {
	data, err := json.Marshal(subs)
	if err != nil {
		return fmt.Errorf("marshal subscriptions: %w", err)
	}
	if err := s.store.Set(ctx, subscriptionsStoreKey, data); err != nil {
		return fmt.Errorf("save subscriptions: %w", err)
	}
	return nil
}
