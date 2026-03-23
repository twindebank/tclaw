package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/libraries/store"
)

const dynamicChannelsStoreKey = "dynamic_channels"

// DynamicChannelConfig is a user-created channel stored in the per-user store.
// Secrets (e.g. Telegram bot tokens) are stored separately in the secret store,
// not in this config. Use ChannelSecretKey to derive the secret store key.
type DynamicChannelConfig struct {
	Name        string      `json:"name"`
	Type        ChannelType `json:"type"`
	Description string      `json:"description"`
	CreatedAt   time.Time   `json:"created_at"`

	// AllowedTools is the resolved set of tools this channel can use.
	// Populated at creation time from tool_groups or explicit lists.
	AllowedTools []string `json:"allowed_tools,omitempty"`

	// DisallowedTools are tools explicitly denied on this channel.
	DisallowedTools []string `json:"disallowed_tools,omitempty"`

	// AllowedUsers restricts which Telegram user IDs can interact with this channel.
	// At least one user ID is required for Telegram channels — messages from users
	// not in this list are silently ignored.
	AllowedUsers []int64 `json:"allowed_users,omitempty"`

	// NotifyLifecycle sends a message to this channel on instance startup and shutdown.
	NotifyLifecycle bool `json:"notify_lifecycle,omitempty"`

	// Links declares which channels this channel can send messages to via
	// the channel_send MCP tool.
	Links []Link `json:"links,omitempty"`

	// CreatableGroups is the set of tool group names this channel can delegate
	// when creating new channels via channel_create. If empty, channel_create
	// is blocked. Prevents privilege escalation.
	CreatableGroups []string `json:"creatable_groups,omitempty"`

	// Ephemeral marks this channel for automatic cleanup after idle timeout.
	Ephemeral bool `json:"ephemeral,omitempty"`

	// EphemeralIdleTimeout is how long an ephemeral channel can sit idle
	// before auto-cleanup. Defaults to 24 hours. Only meaningful when
	// Ephemeral is true.
	EphemeralIdleTimeout time.Duration `json:"ephemeral_idle_timeout,omitempty"`

	// TeardownState holds platform-specific state needed to clean up
	// resources when the channel is deleted (e.g. Telegram bot username).
	// Nil for channels with no platform resources to clean up.
	// Serialized via MarshalTeardownState/UnmarshalTeardownState.
	TeardownState TeardownState `json:"-"`

	// TeardownStateRaw is the JSON-serialized form of TeardownState, used
	// for transparent JSON round-tripping via the store. Callers should use
	// TeardownState (the typed field) instead of this.
	TeardownStateRaw json.RawMessage `json:"teardown_state,omitempty"`
}

// MarshalJSON implements json.Marshaler to serialize TeardownState via the
// typed envelope format.
func (c DynamicChannelConfig) MarshalJSON() ([]byte, error) {
	// Marshal TeardownState into the raw field before encoding.
	type Alias DynamicChannelConfig
	raw, err := MarshalTeardownState(c.TeardownState)
	if err != nil {
		return nil, fmt.Errorf("marshal teardown state: %w", err)
	}
	c.TeardownStateRaw = raw
	return json.Marshal((*Alias)(&c))
}

// UnmarshalJSON implements json.Unmarshaler to deserialize TeardownState
// from the typed envelope format.
func (c *DynamicChannelConfig) UnmarshalJSON(data []byte) error {
	type Alias DynamicChannelConfig
	if err := json.Unmarshal(data, (*Alias)(c)); err != nil {
		return err
	}
	if len(c.TeardownStateRaw) > 0 {
		ts, err := UnmarshalTeardownState(c.TeardownStateRaw)
		if err != nil {
			return fmt.Errorf("unmarshal teardown state: %w", err)
		}
		c.TeardownState = ts
	}
	return nil
}

// ChannelSecretKey returns the secret store key for a channel's secret (e.g. bot token).
func ChannelSecretKey(channelName string) string {
	return "channel/" + channelName + "/token"
}

// DynamicStore manages CRUD for user-created channel configs.
// Follows the same JSON-array-in-a-single-key pattern as connection.Manager.
type DynamicStore struct {
	store store.Store
}

// NewDynamicStore creates a dynamic channel store backed by the given store.
func NewDynamicStore(s store.Store) *DynamicStore {
	return &DynamicStore{store: s}
}

// List returns all dynamic channel configs.
func (d *DynamicStore) List(ctx context.Context) ([]DynamicChannelConfig, error) {
	data, err := d.store.Get(ctx, dynamicChannelsStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read dynamic channels: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	var configs []DynamicChannelConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return nil, fmt.Errorf("parse dynamic channels: %w", err)
	}
	return configs, nil
}

// Get returns a single dynamic channel config by name, or nil if not found.
func (d *DynamicStore) Get(ctx context.Context, name string) (*DynamicChannelConfig, error) {
	configs, err := d.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, cfg := range configs {
		if cfg.Name == name {
			return &cfg, nil
		}
	}
	return nil, nil
}

// Add creates a new dynamic channel config. Returns an error if one with the same name exists.
func (d *DynamicStore) Add(ctx context.Context, cfg DynamicChannelConfig) error {
	configs, err := d.List(ctx)
	if err != nil {
		return err
	}

	for _, existing := range configs {
		if existing.Name == cfg.Name {
			return fmt.Errorf("dynamic channel %q already exists", cfg.Name)
		}
	}

	configs = append(configs, cfg)
	return d.save(ctx, configs)
}

// Update replaces the config for an existing dynamic channel by name.
func (d *DynamicStore) Update(ctx context.Context, name string, updateFn func(*DynamicChannelConfig)) error {
	configs, err := d.List(ctx)
	if err != nil {
		return err
	}

	found := false
	for i := range configs {
		if configs[i].Name == name {
			updateFn(&configs[i])
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("dynamic channel %q not found", name)
	}

	return d.save(ctx, configs)
}

// Remove deletes a dynamic channel config by name.
func (d *DynamicStore) Remove(ctx context.Context, name string) error {
	configs, err := d.List(ctx)
	if err != nil {
		return err
	}

	found := false
	var remaining []DynamicChannelConfig
	for _, cfg := range configs {
		if cfg.Name == name {
			found = true
			continue
		}
		remaining = append(remaining, cfg)
	}
	if !found {
		return fmt.Errorf("dynamic channel %q not found", name)
	}

	return d.save(ctx, remaining)
}

func (d *DynamicStore) save(ctx context.Context, configs []DynamicChannelConfig) error {
	data, err := json.Marshal(configs)
	if err != nil {
		return fmt.Errorf("marshal dynamic channels: %w", err)
	}
	if err := d.store.Set(ctx, dynamicChannelsStoreKey, data); err != nil {
		return fmt.Errorf("save dynamic channels: %w", err)
	}
	return nil
}
