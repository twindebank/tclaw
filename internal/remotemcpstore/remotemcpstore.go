// Package remotemcpstore manages storage for remote MCP server configurations.
// Extracted from the connection package to break the dependency on the legacy
// connection/provider system.
package remotemcpstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
)

const (
	remoteMCPsStoreKey    = "remote_mcps"
	remoteMCPAuthKeyPrefix = "remote_mcp/"
)

// RemoteMCP is a remote MCP server the user has connected.
type RemoteMCP struct {
	Name    string    `json:"name"`
	URL     string    `json:"url"`
	Channel string    `json:"channel"`
	CreatedAt time.Time `json:"created_at"`
}

// RemoteMCPAuth holds OAuth credentials and registration for a remote MCP.
type RemoteMCPAuth struct {
	AuthServerIssuer      string `json:"auth_server_issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint,omitempty"`

	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`

	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenExpiry  time.Time `json:"token_expiry,omitempty"`
}

// TokenExpired reports whether the access token has expired (with 1-minute buffer).
func (a RemoteMCPAuth) TokenExpired() bool {
	if a.TokenExpiry.IsZero() {
		return false
	}
	return time.Now().After(a.TokenExpiry.Add(-1 * time.Minute))
}

// Manager handles CRUD for remote MCP servers and their auth credentials.
type Manager struct {
	store   store.Store
	secrets secret.Store
}

// NewManager creates a remote MCP manager backed by the given stores.
func NewManager(s store.Store, sec secret.Store) *Manager {
	return &Manager{store: s, secrets: sec}
}

func (m *Manager) ListRemoteMCPs(ctx context.Context) ([]RemoteMCP, error) {
	data, err := m.store.Get(ctx, remoteMCPsStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read remote mcps: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var mcps []RemoteMCP
	if err := json.Unmarshal(data, &mcps); err != nil {
		return nil, fmt.Errorf("parse remote mcps: %w", err)
	}
	return mcps, nil
}

func (m *Manager) GetRemoteMCP(ctx context.Context, name string) (*RemoteMCP, error) {
	mcps, err := m.ListRemoteMCPs(ctx)
	if err != nil {
		return nil, err
	}
	for _, mcp := range mcps {
		if mcp.Name == name {
			return &mcp, nil
		}
	}
	return nil, nil
}

func (m *Manager) AddRemoteMCP(ctx context.Context, name, url, channel string) (*RemoteMCP, error) {
	mcps, err := m.ListRemoteMCPs(ctx)
	if err != nil {
		return nil, err
	}
	for _, mcp := range mcps {
		if mcp.Name == name {
			return nil, fmt.Errorf("remote mcp %q already exists", name)
		}
	}
	entry := RemoteMCP{
		Name:      name,
		URL:       url,
		Channel:   channel,
		CreatedAt: time.Now(),
	}
	mcps = append(mcps, entry)
	if err := m.saveRemoteMCPs(ctx, mcps); err != nil {
		return nil, err
	}
	return &entry, nil
}

func (m *Manager) ListRemoteMCPsByChannel(ctx context.Context, channelName string) ([]RemoteMCP, error) {
	mcps, err := m.ListRemoteMCPs(ctx)
	if err != nil {
		return nil, err
	}
	var result []RemoteMCP
	for _, mcp := range mcps {
		if mcp.Channel == channelName {
			result = append(result, mcp)
		}
	}
	return result, nil
}

func (m *Manager) RemoveRemoteMCP(ctx context.Context, name string) error {
	mcps, err := m.ListRemoteMCPs(ctx)
	if err != nil {
		return err
	}
	found := false
	var remaining []RemoteMCP
	for _, mcp := range mcps {
		if mcp.Name == name {
			found = true
			continue
		}
		remaining = append(remaining, mcp)
	}
	if !found {
		return fmt.Errorf("remote mcp %q not found", name)
	}
	if err := m.secrets.Delete(ctx, remoteMCPAuthKey(name)); err != nil {
		return fmt.Errorf("delete remote mcp auth: %w", err)
	}
	if err := m.saveRemoteMCPs(ctx, remaining); err != nil {
		return err
	}
	return nil
}

func (m *Manager) GetRemoteMCPAuth(ctx context.Context, name string) (*RemoteMCPAuth, error) {
	val, err := m.secrets.Get(ctx, remoteMCPAuthKey(name))
	if err != nil {
		return nil, fmt.Errorf("read remote mcp auth: %w", err)
	}
	if val == "" {
		return nil, nil
	}
	var auth RemoteMCPAuth
	if err := json.Unmarshal([]byte(val), &auth); err != nil {
		return nil, fmt.Errorf("parse remote mcp auth: %w", err)
	}
	return &auth, nil
}

func (m *Manager) SetRemoteMCPAuth(ctx context.Context, name string, auth *RemoteMCPAuth) error {
	data, err := json.Marshal(auth)
	if err != nil {
		return fmt.Errorf("marshal remote mcp auth: %w", err)
	}
	if err := m.secrets.Set(ctx, remoteMCPAuthKey(name), string(data)); err != nil {
		return fmt.Errorf("store remote mcp auth: %w", err)
	}
	return nil
}

func (m *Manager) saveRemoteMCPs(ctx context.Context, mcps []RemoteMCP) error {
	data, err := json.Marshal(mcps)
	if err != nil {
		return fmt.Errorf("marshal remote mcps: %w", err)
	}
	if err := m.store.Set(ctx, remoteMCPsStoreKey, data); err != nil {
		return fmt.Errorf("save remote mcps: %w", err)
	}
	return nil
}

func remoteMCPAuthKey(name string) string {
	return remoteMCPAuthKeyPrefix + name
}
