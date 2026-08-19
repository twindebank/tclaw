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

	// PendingAction is set when the agent has asked the user to confirm
	// something and sent the prompt. The router intercepts the next inbound
	// message: "yes" performs the action, anything else clears it.
	PendingAction *PendingAction `json:"pending_action,omitempty"`

	// LastMessageAt is the time the most recent inbound message was received.
	// Persisted so ephemeral cleanup can survive process restarts.
	LastMessageAt time.Time `json:"last_message_at,omitempty"`

	// LastMessageSource is who sent the most recent message (e.g. "user", "schedule").
	// Persisted alongside LastMessageAt for observability.
	LastMessageSource MessageSource `json:"last_message_source,omitempty"`
}

// PendingActionKind identifies what a pending confirmation will do.
type PendingActionKind string

const (
	// PendingChannelDone tears the channel down.
	PendingChannelDone PendingActionKind = "channel_done"

	// PendingRepoGrant raises a repo's access tier.
	PendingRepoGrant PendingActionKind = "repo_grant"

	// PendingRuleWrite writes a proposed rulebook once the user approves it.
	PendingRuleWrite PendingActionKind = "rule_write"
)

// PendingAction is a confirmation the user has been asked for but has not yet
// answered. It is the one boundary the agent cannot cross on its own: the
// prompt is sent straight to the chat rather than through the agent, and only a
// genuine user reply confirms it.
type PendingAction struct {
	// Kind selects which handler runs on confirmation.
	Kind PendingActionKind `json:"kind"`

	// Payload carries the kind's own parameters (e.g. which repo and tier).
	// Opaque here so channel doesn't need to know about every caller.
	Payload json.RawMessage `json:"payload,omitempty"`

	// RequestedAt is when the prompt was sent.
	RequestedAt time.Time `json:"requested_at"`

	// ExpiresAt is when the confirmation stops being valid. A "yes" arriving
	// after this is treated as an ordinary message, so a stale prompt answered
	// hours later cannot silently grant something.
	ExpiresAt time.Time `json:"expires_at"`
}

// Expired reports whether the confirmation window has passed.
func (p PendingAction) Expired(now time.Time) bool {
	return !p.ExpiresAt.IsZero() && now.After(p.ExpiresAt)
}

// PendingConfirmationTTL is how long a confirmation prompt stays answerable.
// Long enough for a reply from a phone, short enough that a "yes" typed into a
// forgotten thread the next day doesn't act on a prompt the user has lost track of.
const PendingConfirmationTTL = 30 * time.Minute

// NewPendingAction builds a pending action armed for the standard window.
func NewPendingAction(kind PendingActionKind, payload json.RawMessage) *PendingAction {
	now := time.Now()
	return &PendingAction{
		Kind:        kind,
		Payload:     payload,
		RequestedAt: now,
		ExpiresAt:   now.Add(PendingConfirmationTTL),
	}
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
