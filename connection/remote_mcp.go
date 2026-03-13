package connection

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const remoteMCPsStoreKey = "remote_mcps"
const remoteMCPAuthKeyPrefix = "remote_mcp/"

// RemoteMCP is a remote MCP server the user has connected.
// Stored in the regular store, not the secret store.
type RemoteMCP struct {
	Name string `json:"name"` // user-chosen label ("linear", "notion")
	URL  string `json:"url"`  // MCP server endpoint URL

	// Channel scopes this remote MCP to a specific channel. The remote MCP's
	// tools are only included in that channel's MCP config.
	Channel string `json:"channel"`

	CreatedAt time.Time `json:"created_at"`
}

// RemoteMCPAuth holds OAuth credentials and registration for a remote MCP.
// Stored in the secret store.
type RemoteMCPAuth struct {
	// OAuth server discovery
	AuthServerIssuer      string `json:"auth_server_issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint,omitempty"`

	// Dynamic client registration
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`

	// Tokens
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

// ListRemoteMCPs returns all remote MCP servers for this user.
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

// GetRemoteMCP returns a single remote MCP by name, or nil if not found.
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

// AddRemoteMCP creates a new remote MCP entry scoped to a channel. Returns an
// error if one with the same name exists. The channel parameter associates the
// remote MCP with a specific channel — its tools are only available on that
// channel.
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

// ListRemoteMCPsByChannel returns remote MCPs scoped to the given channel.
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

// RemoveRemoteMCP deletes a remote MCP and its auth credentials.
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

	// Delete auth first so we don't leave orphans on save failure.
	if err := m.secrets.Delete(ctx, remoteMCPAuthKey(name)); err != nil {
		return fmt.Errorf("delete remote mcp auth: %w", err)
	}

	if err := m.saveRemoteMCPs(ctx, remaining); err != nil {
		return err
	}
	return nil
}

// GetRemoteMCPAuth loads stored OAuth credentials for a remote MCP.
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

// SetRemoteMCPAuth stores OAuth credentials for a remote MCP.
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
