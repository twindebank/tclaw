package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"tclaw/internal/libraries/store"
)

const runtimeStateKeyPrefix = "channel_runtime/"

// RuntimeState holds transient per-channel state that persists across agent
// restarts but does not belong in the config file. This includes platform-
// specific metadata (Telegram chat IDs), teardown info, and async flow flags.
type RuntimeState struct {
	// PlatformState holds platform-specific metadata (e.g. Telegram chat ID).
	PlatformState PlatformState `json:"platform_state,omitempty"`

	// TeardownState holds platform-specific cleanup info (e.g. Telegram bot username).
	TeardownState TeardownState `json:"teardown_state,omitempty"`

	// PendingDone is true when the agent has called channel_done and sent a
	// confirmation prompt. The router intercepts the next inbound message:
	// "yes" triggers teardown, anything else clears the flag.
	PendingDone bool `json:"pending_done,omitempty"`

	// LastMessageAt is the time the most recent inbound message was received.
	// Persisted so ephemeral cleanup can survive process restarts.
	LastMessageAt time.Time `json:"last_message_at,omitempty"`

	// LastMessageSource is who sent the most recent message (e.g. "user", "schedule").
	// Persisted alongside LastMessageAt for observability.
	LastMessageSource MessageSource `json:"last_message_source,omitempty"`
}

// RuntimeStateStore manages per-channel runtime state backed by the user's
// state store. Each channel gets its own key: "channel_runtime/<name>".
type RuntimeStateStore struct {
	mu    sync.Mutex
	store store.Store
}

// NewRuntimeStateStore creates a runtime state store backed by the given store.
func NewRuntimeStateStore(s store.Store) *RuntimeStateStore {
	return &RuntimeStateStore{store: s}
}

// Get returns the runtime state for a channel, or an empty state if none exists.
func (r *RuntimeStateStore) Get(ctx context.Context, name string) (*RuntimeState, error) {
	data, err := r.store.Get(ctx, runtimeStateKeyPrefix+name)
	if err != nil {
		return nil, fmt.Errorf("read runtime state for %q: %w", name, err)
	}
	if len(data) == 0 {
		return &RuntimeState{}, nil
	}

	var state RuntimeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse runtime state for %q: %w", name, err)
	}
	return &state, nil
}

// Update applies fn to the runtime state for a channel and saves the result.
// If no state exists yet, fn receives a zero-value RuntimeState.
func (r *RuntimeStateStore) Update(ctx context.Context, name string, fn func(*RuntimeState)) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	state, err := r.get(ctx, name)
	if err != nil {
		return err
	}

	fn(state)

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal runtime state for %q: %w", name, err)
	}
	if err := r.store.Set(ctx, runtimeStateKeyPrefix+name, data); err != nil {
		return fmt.Errorf("save runtime state for %q: %w", name, err)
	}
	return nil
}

// Delete removes the runtime state for a channel.
func (r *RuntimeStateStore) Delete(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.store.Delete(ctx, runtimeStateKeyPrefix+name); err != nil {
		return fmt.Errorf("delete runtime state for %q: %w", name, err)
	}
	return nil
}

// get is the internal reader called under the mutex by Update.
func (r *RuntimeStateStore) get(ctx context.Context, name string) (*RuntimeState, error) {
	data, err := r.store.Get(ctx, runtimeStateKeyPrefix+name)
	if err != nil {
		return nil, fmt.Errorf("read runtime state for %q: %w", name, err)
	}
	if len(data) == 0 {
		return &RuntimeState{}, nil
	}

	var state RuntimeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse runtime state for %q: %w", name, err)
	}
	return &state, nil
}
