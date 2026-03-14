package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/libraries/store"
	"tclaw/role"
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

	// Role is a named preset of tool permissions for this channel.
	// Mutually exclusive with AllowedTools.
	Role role.Role `json:"role,omitempty"`

	// AllowedTools overrides user-level tool permissions for this channel.
	AllowedTools []string `json:"allowed_tools,omitempty"`

	// DisallowedTools overrides user-level tool permissions for this channel.
	DisallowedTools []string `json:"disallowed_tools,omitempty"`

	// AllowedUsers restricts which Telegram user IDs can interact with this channel.
	// At least one user ID is required for Telegram channels — messages from users
	// not in this list are silently ignored.
	AllowedUsers []int64 `json:"allowed_users,omitempty"`
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
