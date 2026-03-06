package connection

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/provider"
)

const connectionsStoreKey = "connections"
const credentialKeyPrefix = "conn/"

// Manager handles CRUD for connections and their credentials.
type Manager struct {
	store   store.Store
	secrets secret.Store
}

// NewManager creates a connection manager backed by the given stores.
func NewManager(s store.Store, sec secret.Store) *Manager {
	return &Manager{store: s, secrets: sec}
}

// List returns all connections for this user.
func (m *Manager) List(ctx context.Context) ([]Connection, error) {
	data, err := m.store.Get(ctx, connectionsStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read connections: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	var conns []Connection
	if err := json.Unmarshal(data, &conns); err != nil {
		return nil, fmt.Errorf("parse connections: %w", err)
	}
	return conns, nil
}

// Get returns a single connection by ID, or nil if not found.
func (m *Manager) Get(ctx context.Context, id ConnectionID) (*Connection, error) {
	conns, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range conns {
		if c.ID == id {
			return &c, nil
		}
	}
	return nil, nil
}

// Add creates a new connection. Returns an error if one with the same ID exists.
func (m *Manager) Add(ctx context.Context, providerID provider.ProviderID, label string) (*Connection, error) {
	conns, err := m.List(ctx)
	if err != nil {
		return nil, err
	}

	id := NewConnectionID(providerID, label)
	for _, c := range conns {
		if c.ID == id {
			return nil, fmt.Errorf("connection %q already exists", id)
		}
	}

	conn := Connection{
		ID:         id,
		ProviderID: providerID,
		Label:      label,
		CreatedAt:  time.Now(),
	}
	conns = append(conns, conn)

	if err := m.saveConnections(ctx, conns); err != nil {
		return nil, err
	}
	return &conn, nil
}

// Remove deletes a connection and its credentials.
func (m *Manager) Remove(ctx context.Context, id ConnectionID) error {
	conns, err := m.List(ctx)
	if err != nil {
		return err
	}

	found := false
	var remaining []Connection
	for _, c := range conns {
		if c.ID == id {
			found = true
			continue
		}
		remaining = append(remaining, c)
	}
	if !found {
		return fmt.Errorf("connection %q not found", id)
	}

	// Delete credentials first so we don't leave orphans on save failure.
	if err := m.secrets.Delete(ctx, credentialKey(id)); err != nil {
		return fmt.Errorf("delete credentials: %w", err)
	}

	if err := m.saveConnections(ctx, remaining); err != nil {
		return err
	}
	return nil
}

// GetCredentials loads stored credentials for a connection.
func (m *Manager) GetCredentials(ctx context.Context, id ConnectionID) (*Credentials, error) {
	val, err := m.secrets.Get(ctx, credentialKey(id))
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	if val == "" {
		return nil, nil
	}

	var creds Credentials
	if err := json.Unmarshal([]byte(val), &creds); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	return &creds, nil
}

// SetCredentials stores credentials for a connection.
func (m *Manager) SetCredentials(ctx context.Context, id ConnectionID, creds *Credentials) error {
	data, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	if err := m.secrets.Set(ctx, credentialKey(id), string(data)); err != nil {
		return fmt.Errorf("store credentials: %w", err)
	}
	return nil
}

// RefreshIfNeeded returns valid credentials, refreshing the access token if expired.
// Requires the provider's OAuth config for token refresh.
// Returns an error if no credentials exist or refresh fails.
func (m *Manager) RefreshIfNeeded(ctx context.Context, id ConnectionID, refreshFn func(ctx context.Context, refreshToken string) (*Credentials, error)) (*Credentials, error) {
	creds, err := m.GetCredentials(ctx, id)
	if err != nil {
		return nil, err
	}
	if creds == nil {
		return nil, fmt.Errorf("no credentials for connection %q", id)
	}

	if !creds.Expired() {
		return creds, nil
	}

	if creds.RefreshToken == "" {
		return nil, fmt.Errorf("credentials for %q expired and no refresh token available", id)
	}

	newCreds, err := refreshFn(ctx, creds.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}

	// Preserve the refresh token if the new response doesn't include one.
	if newCreds.RefreshToken == "" {
		newCreds.RefreshToken = creds.RefreshToken
	}

	if err := m.SetCredentials(ctx, id, newCreds); err != nil {
		return nil, err
	}
	return newCreds, nil
}

func (m *Manager) saveConnections(ctx context.Context, conns []Connection) error {
	data, err := json.Marshal(conns)
	if err != nil {
		return fmt.Errorf("marshal connections: %w", err)
	}
	if err := m.store.Set(ctx, connectionsStoreKey, data); err != nil {
		return fmt.Errorf("save connections: %w", err)
	}
	return nil
}

func credentialKey(id ConnectionID) string {
	return credentialKeyPrefix + string(id)
}
