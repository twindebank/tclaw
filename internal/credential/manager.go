// Package credential provides unified credential storage for tool packages. A CredentialSet is a
// named, optionally channel-scoped collection of secrets (API keys, OAuth tokens). Manager handles
// CRUD for credential sets, individual secret fields, and OAuth token lifecycle including refresh.
package credential

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
)

const (
	setsStoreKey  = "credential_sets"
	credKeyPrefix = "cred/"
	oauthFieldKey = "_oauth"
)

// RefreshFunc exchanges a refresh token for new OAuth tokens.
type RefreshFunc func(ctx context.Context, refreshToken string) (*OAuthTokens, error)

// Manager handles CRUD for credential sets and their secrets.
type Manager struct {
	store   store.Store
	secrets secret.Store
}

// NewManager creates a credential manager backed by the given stores.
func NewManager(s store.Store, sec secret.Store) *Manager {
	return &Manager{store: s, secrets: sec}
}

// List returns all credential sets.
func (m *Manager) List(ctx context.Context) ([]CredentialSet, error) {
	data, err := m.store.Get(ctx, setsStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read credential sets: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var sets []CredentialSet
	if err := json.Unmarshal(data, &sets); err != nil {
		return nil, fmt.Errorf("parse credential sets: %w", err)
	}
	return sets, nil
}

// ListByPackage returns credential sets for a specific tool package.
func (m *Manager) ListByPackage(ctx context.Context, packageName string) ([]CredentialSet, error) {
	sets, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []CredentialSet
	for _, s := range sets {
		if s.Package == packageName {
			result = append(result, s)
		}
	}
	return result, nil
}

// ListByChannel returns credential sets scoped to a specific channel, plus
// any sets with no channel scope (available everywhere).
func (m *Manager) ListByChannel(ctx context.Context, channelName string) ([]CredentialSet, error) {
	sets, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []CredentialSet
	for _, s := range sets {
		if s.Channel == "" || s.Channel == channelName {
			result = append(result, s)
		}
	}
	return result, nil
}

// Get returns a single credential set by ID, or nil if not found.
func (m *Manager) Get(ctx context.Context, id CredentialSetID) (*CredentialSet, error) {
	sets, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range sets {
		if s.ID == id {
			return &s, nil
		}
	}
	return nil, nil
}

// AddParams describes a credential set to create.
type AddParams struct {
	// Package is the set's type: a tool package name, or "git".
	Package string

	// Label distinguishes sets of the same type.
	Label string

	// Channel restricts the set to one channel. Empty means all channels.
	Channel string

	// Description explains what the credential is for. Surfaced by credential_list.
	Description string
}

// Add creates a new credential set. Returns an error if one with the same ID exists.
func (m *Manager) Add(ctx context.Context, params AddParams) (*CredentialSet, error) {
	sets, err := m.List(ctx)
	if err != nil {
		return nil, err
	}

	id := NewCredentialSetID(params.Package, params.Label)
	for _, s := range sets {
		if s.ID == id {
			return nil, fmt.Errorf("credential set %q already exists", id)
		}
	}

	set := CredentialSet{
		ID:          id,
		Package:     params.Package,
		Label:       params.Label,
		Channel:     params.Channel,
		Description: params.Description,
		CreatedAt:   time.Now(),
	}
	sets = append(sets, set)

	if err := m.saveSets(ctx, sets); err != nil {
		return nil, err
	}
	return &set, nil
}

// Remove deletes a credential set and its stored OAuth tokens.
func (m *Manager) Remove(ctx context.Context, id CredentialSetID) error {
	sets, err := m.List(ctx)
	if err != nil {
		return err
	}

	found := false
	var remaining []CredentialSet
	for _, s := range sets {
		if s.ID == id {
			found = true
			continue
		}
		remaining = append(remaining, s)
	}
	if !found {
		return fmt.Errorf("credential set %q not found", id)
	}

	// Best-effort cleanup of the OAuth token blob.
	if err := m.secrets.Delete(ctx, fieldKey(id, oauthFieldKey)); err != nil {
		// Non-fatal: the token blob may not exist.
	}

	if err := m.saveSets(ctx, remaining); err != nil {
		return err
	}
	return nil
}

func (m *Manager) saveSets(ctx context.Context, sets []CredentialSet) error {
	data, err := json.Marshal(sets)
	if err != nil {
		return fmt.Errorf("marshal credential sets: %w", err)
	}
	if err := m.store.Set(ctx, setsStoreKey, data); err != nil {
		return fmt.Errorf("save credential sets: %w", err)
	}
	return nil
}

func fieldKey(id CredentialSetID, field string) string {
	return credKeyPrefix + string(id) + "/" + field
}

// FieldKey returns the secret store key a credential set's field is stored under.
// Exported for callers that must address the key directly (e.g. secret forms writing
// into a declared slot) rather than going through SetField.
func FieldKey(id CredentialSetID, field string) string {
	return fieldKey(id, field)
}
