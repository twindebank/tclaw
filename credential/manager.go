package credential

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/libraries/secret"
	"tclaw/libraries/store"
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

// Add creates a new credential set. Returns an error if one with the same ID exists.
func (m *Manager) Add(ctx context.Context, packageName string, label string, channel string) (*CredentialSet, error) {
	sets, err := m.List(ctx)
	if err != nil {
		return nil, err
	}

	id := NewCredentialSetID(packageName, label)
	for _, s := range sets {
		if s.ID == id {
			return nil, fmt.Errorf("credential set %q already exists", id)
		}
	}

	set := CredentialSet{
		ID:        id,
		Package:   packageName,
		Label:     label,
		Channel:   channel,
		CreatedAt: time.Now(),
	}
	sets = append(sets, set)

	if err := m.saveSets(ctx, sets); err != nil {
		return nil, err
	}
	return &set, nil
}

// Remove deletes a credential set and all its stored secrets.
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

	// Best-effort cleanup of stored secrets. Individual field keys follow the
	// pattern cred/<package>/<label>/<field>, but we don't track which fields
	// exist. The OAuth token blob has a known key, so delete that.
	if err := m.secrets.Delete(ctx, fieldKey(id, oauthFieldKey)); err != nil {
		// Non-fatal: the token blob may not exist.
	}

	if err := m.saveSets(ctx, remaining); err != nil {
		return err
	}
	return nil
}

// SetField stores a single credential field value.
func (m *Manager) SetField(ctx context.Context, id CredentialSetID, field string, value string) error {
	if err := m.secrets.Set(ctx, fieldKey(id, field), value); err != nil {
		return fmt.Errorf("store field %q for %s: %w", field, id, err)
	}
	return nil
}

// GetField retrieves a single credential field value. Returns empty string if
// the field is not set.
func (m *Manager) GetField(ctx context.Context, id CredentialSetID, field string) (string, error) {
	val, err := m.secrets.Get(ctx, fieldKey(id, field))
	if err != nil {
		return "", fmt.Errorf("read field %q for %s: %w", field, id, err)
	}
	return val, nil
}

// GetAllFields returns all stored fields for a credential set by checking each
// field key in the provided list.
func (m *Manager) GetAllFields(ctx context.Context, id CredentialSetID, fieldKeys []string) (map[string]string, error) {
	result := make(map[string]string, len(fieldKeys))
	for _, key := range fieldKeys {
		val, err := m.GetField(ctx, id, key)
		if err != nil {
			return nil, err
		}
		if val != "" {
			result[key] = val
		}
	}
	return result, nil
}

// SetOAuthTokens stores OAuth tokens for a credential set.
func (m *Manager) SetOAuthTokens(ctx context.Context, id CredentialSetID, tokens *OAuthTokens) error {
	data, err := json.Marshal(tokens)
	if err != nil {
		return fmt.Errorf("marshal oauth tokens: %w", err)
	}
	if err := m.secrets.Set(ctx, fieldKey(id, oauthFieldKey), string(data)); err != nil {
		return fmt.Errorf("store oauth tokens for %s: %w", id, err)
	}
	return nil
}

// GetOAuthTokens retrieves OAuth tokens for a credential set. Returns nil if
// no tokens are stored.
func (m *Manager) GetOAuthTokens(ctx context.Context, id CredentialSetID) (*OAuthTokens, error) {
	val, err := m.secrets.Get(ctx, fieldKey(id, oauthFieldKey))
	if err != nil {
		return nil, fmt.Errorf("read oauth tokens for %s: %w", id, err)
	}
	if val == "" {
		return nil, nil
	}

	var tokens OAuthTokens
	if err := json.Unmarshal([]byte(val), &tokens); err != nil {
		return nil, fmt.Errorf("parse oauth tokens for %s: %w", id, err)
	}
	return &tokens, nil
}

// RefreshIfNeeded returns valid OAuth tokens, refreshing them if expired.
// Returns an error if no tokens exist or refresh fails.
func (m *Manager) RefreshIfNeeded(ctx context.Context, id CredentialSetID, refreshFn RefreshFunc) (*OAuthTokens, error) {
	tokens, err := m.GetOAuthTokens(ctx, id)
	if err != nil {
		return nil, err
	}
	if tokens == nil {
		return nil, fmt.Errorf("no oauth tokens for credential set %q", id)
	}

	if !tokens.Expired() {
		return tokens, nil
	}

	if tokens.RefreshToken == "" {
		return nil, fmt.Errorf("oauth tokens for %q expired and no refresh token available", id)
	}

	newTokens, err := refreshFn(ctx, tokens.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh oauth tokens: %w", err)
	}

	// Preserve the refresh token if the new response doesn't include one.
	if newTokens.RefreshToken == "" {
		newTokens.RefreshToken = tokens.RefreshToken
	}

	if err := m.SetOAuthTokens(ctx, id, newTokens); err != nil {
		return nil, err
	}
	return newTokens, nil
}

// IsReady checks whether a credential set has all required fields populated.
// For OAuth sets, it also checks that OAuth tokens are present.
func (m *Manager) IsReady(ctx context.Context, id CredentialSetID, requiredFields []string, needsOAuth bool) (bool, error) {
	for _, field := range requiredFields {
		val, err := m.GetField(ctx, id, field)
		if err != nil {
			return false, err
		}
		if val == "" {
			return false, nil
		}
	}

	if needsOAuth {
		tokens, err := m.GetOAuthTokens(ctx, id)
		if err != nil {
			return false, err
		}
		if tokens == nil || tokens.AccessToken == "" {
			return false, nil
		}
	}

	return true, nil
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
