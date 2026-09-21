// Package remotemcpstore manages storage for remote MCP server configurations.
// Extracted from the connection package to break the dependency on the legacy
// connection/provider system.
package remotemcpstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
)

const (
	remoteMCPsStoreKey     = "remote_mcps"
	remoteMCPAuthKeyPrefix = "remote_mcp/"
)

// RemoteMCP is a remote MCP server the user has connected.
type RemoteMCP struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`

	// Channels restricts which channels this server's tools reach. Every listed
	// channel carries it in its own MCP config file and its own tool allowlist.
	// Empty reaches every channel: no tool creates that, and boot reports it.
	Channels []string `json:"channels,omitempty"`

	// URLSensitive is true if the URL was registered via url_secret_key and
	// should be treated as a credential (not echoed to the agent in tool
	// responses). False for URLs passed inline, since those were already
	// visible in the originating tool call.
	URLSensitive bool `json:"url_sensitive,omitempty"`

	// ToolNames is the list of tool names the remote MCP exposed at
	// registration time. The Claude CLI's --allowedTools flag does not
	// honour wildcards for MCP tools, so tclaw must expand glob patterns
	// like `mcp__<server>__*` into explicit tool names at agent-start time.
	// Without this list the CLI refuses every tool call on the server.
	ToolNames []string `json:"tool_names,omitempty"`

	// TLSPinSHA256 pins the server's TLS leaf certificate by its hex SHA-256
	// fingerprint. Set for self-signed servers on Fly private hosts, where no
	// public CA applies — the pin authenticates the server without trusting the
	// network. Non-secret (a public cert hash), so it's stored inline like the
	// URL. Empty means default chain verification.
	TLSPinSHA256 string `json:"tls_pin_sha256,omitempty"`

	// Instructions is the server's self-description captured from the MCP
	// initialize handshake (InitializeResult.instructions) — how to use the
	// server and its features. Surfaced to the agent so it knows how to drive
	// the server (e.g. session lifecycle) instead of guessing. Empty if the
	// server exposed none.
	Instructions string `json:"instructions,omitempty"`
}

// RemoteMCPAuth holds OAuth credentials and registration for a remote MCP,
// plus any non-OAuth credentials such as Cloudflare Access service tokens.
type RemoteMCPAuth struct {
	AuthServerIssuer      string `json:"auth_server_issuer,omitempty"`
	AuthorizationEndpoint string `json:"authorization_endpoint,omitempty"`
	TokenEndpoint         string `json:"token_endpoint,omitempty"`
	RegistrationEndpoint  string `json:"registration_endpoint,omitempty"`

	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`

	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenExpiry  time.Time `json:"token_expiry,omitempty"`

	// PendingExchange holds the in-flight authorization-code state for a
	// manual (loopback) OAuth flow. The user pastes the callback URL back on
	// a later turn, in a different agent subprocess, so the verifier cannot
	// live in memory the way the hosted-callback flow's does. Cleared once
	// the code is exchanged.
	PendingExchange *PendingExchange `json:"pending_exchange,omitempty"`

	// StaticHeaders are sent verbatim on every request. Used for credentials
	// that aren't OAuth Bearer tokens — e.g. Cloudflare Access service tokens
	// (CF-Access-Client-Id / CF-Access-Client-Secret). Stored encrypted because
	// they are secrets.
	StaticHeaders map[string]string `json:"static_headers,omitempty"`
}

// PendingExchange is the state a manual OAuth flow must carry from the turn
// that built the authorization URL to the turn that receives the pasted code.
type PendingExchange struct {
	// CodeVerifier is the PKCE verifier whose challenge went out on the
	// authorization URL. Secret — it is what stops an intercepted code from
	// being redeemed by anyone else.
	CodeVerifier string `json:"code_verifier"`

	// State is the CSRF token embedded in the authorization URL. The pasted
	// callback must echo it back.
	State string `json:"state"`

	// RedirectURI must be replayed verbatim on the token request; the
	// authorization server compares it against the one it authorized.
	RedirectURI string `json:"redirect_uri"`

	StartedAt time.Time `json:"started_at"`
}

// pendingExchangeTTL bounds how long a pasted callback stays redeemable, so a
// forgotten authorization URL cannot be completed days later.
const pendingExchangeTTL = 30 * time.Minute

// Expired reports whether the pending exchange is too old to complete.
func (p PendingExchange) Expired() bool {
	return time.Since(p.StartedAt) > pendingExchangeTTL
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

	// mu serialises the read-modify-write cycles. Every registration lives in
	// one stored list, so two concurrent edits would otherwise lose one of them.
	mu sync.Mutex
}

// NewManager creates a remote MCP manager backed by the given stores.
func NewManager(s store.Store, sec secret.Store) *Manager {
	return &Manager{store: s, secrets: sec}
}

func (m *Manager) ListRemoteMCPs(ctx context.Context) ([]RemoteMCP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.load(ctx)
}

func (m *Manager) GetRemoteMCP(ctx context.Context, name string) (*RemoteMCP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mcps, err := m.load(ctx)
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

// storedRemoteMCP is the on-disk shape. It carries the `channel` key written
// before a registration could name more than one channel.
type storedRemoteMCP struct {
	RemoteMCP
	LegacyChannel string `json:"channel,omitempty"`
}

// namesSingleChannel reports whether this entry still carries the pre-list key.
func (s storedRemoteMCP) namesSingleChannel() bool {
	return s.LegacyChannel != "" && len(s.Channels) == 0
}

// resolve returns the registration in the current shape.
func (s storedRemoteMCP) resolve() RemoteMCP {
	if s.namesSingleChannel() {
		// TODO: drop this once no entry under the remote_mcps store key carries
		// a `channel` field. MigrateChannelScope rewrites them.
		s.Channels = []string{s.LegacyChannel}
	}
	return s.RemoteMCP
}

// MigrateChannelScope rewrites any registration that still names a single
// channel, so the stored file converges on the list shape.
func (m *Manager) MigrateChannelScope(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	stored, err := m.loadStored(ctx)
	if err != nil {
		return err
	}
	mcps := make([]RemoteMCP, 0, len(stored))
	migrated := 0
	for _, s := range stored {
		if s.namesSingleChannel() {
			migrated++
		}
		mcps = append(mcps, s.resolve())
	}
	if migrated == 0 {
		return nil
	}
	slog.Info("rewriting remote mcp channel scope as a list", "migrated", migrated, "registrations", len(mcps))
	return m.save(ctx, mcps)
}

// loadStored reads the registrations in their on-disk shape. Callers must hold mu.
func (m *Manager) loadStored(ctx context.Context) ([]storedRemoteMCP, error) {
	data, err := m.store.Get(ctx, remoteMCPsStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read remote mcps: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var stored []storedRemoteMCP
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("parse remote mcps: %w", err)
	}
	return stored, nil
}

// load reads the stored registrations in the current shape. Callers must hold mu.
func (m *Manager) load(ctx context.Context) ([]RemoteMCP, error) {
	stored, err := m.loadStored(ctx)
	if err != nil {
		return nil, err
	}
	if len(stored) == 0 {
		return nil, nil
	}
	mcps := make([]RemoteMCP, 0, len(stored))
	for _, s := range stored {
		mcps = append(mcps, s.resolve())
	}
	return mcps, nil
}

// AddRemoteMCPParams configures a new remote MCP registration.
type AddRemoteMCPParams struct {
	Name string
	URL  string

	// Channels restricts which channels the server's tools reach. See
	// RemoteMCP.Channels. Empty means every channel.
	Channels []string

	// URLSensitive marks the URL as a credential so tool responses and list
	// output show only scheme+host, not the full path. Set when the URL was
	// registered via url_secret_key.
	URLSensitive bool

	// ToolNames is the list of tool names the remote MCP server exposed at
	// registration time (discovered via MCP tools/list). Required — without
	// it the Claude CLI can't expand the tool permission glob for this
	// server and will refuse every tool call.
	ToolNames []string

	// TLSPinSHA256 pins the server's TLS certificate by hex SHA-256 fingerprint.
	// Empty means default chain verification. See RemoteMCP.TLSPinSHA256.
	TLSPinSHA256 string

	// Instructions is the server's self-description from the MCP initialize
	// handshake. See RemoteMCP.Instructions. Empty if the server exposed none.
	Instructions string
}

func (m *Manager) AddRemoteMCP(ctx context.Context, p AddRemoteMCPParams) (*RemoteMCP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mcps, err := m.load(ctx)
	if err != nil {
		return nil, err
	}
	for _, mcp := range mcps {
		if mcp.Name == p.Name {
			return nil, fmt.Errorf("remote mcp %q already exists", p.Name)
		}
	}
	entry := RemoteMCP{
		Name:         p.Name,
		URL:          p.URL,
		Channels:     p.Channels,
		CreatedAt:    time.Now(),
		URLSensitive: p.URLSensitive,
		ToolNames:    p.ToolNames,
		TLSPinSHA256: p.TLSPinSHA256,
		Instructions: p.Instructions,
	}
	mcps = append(mcps, entry)
	if err := m.save(ctx, mcps); err != nil {
		return nil, err
	}
	return &entry, nil
}

// SetToolNames updates the stored tool-name list for an existing remote MCP.
// Used by the OAuth and no-auth registration paths where tool discovery
// happens after the entry has already been persisted.
func (m *Manager) SetToolNames(ctx context.Context, name string, toolNames []string) error {
	return m.update(ctx, name, func(mcp *RemoteMCP) { mcp.ToolNames = toolNames })
}

// SetInstructions updates the stored server instructions for an existing remote
// MCP. Used by the OAuth and no-auth registration paths where the initialize
// handshake runs after the entry has already been persisted. An empty string is
// a valid value — it clears any prior instructions when the server exposes none.
func (m *Manager) SetInstructions(ctx context.Context, name string, instructions string) error {
	return m.update(ctx, name, func(mcp *RemoteMCP) { mcp.Instructions = instructions })
}

// SetChannels replaces the set of channels an existing remote MCP's tools reach.
// An empty list means every channel.
func (m *Manager) SetChannels(ctx context.Context, name string, channels []string) error {
	return m.update(ctx, name, func(mcp *RemoteMCP) { mcp.Channels = channels })
}

// PruneChannelResult is what removing a channel from every registration did.
type PruneChannelResult struct {
	// Pruned names the registrations the channel was taken off.
	Pruned []string

	// LeftAlone names the registrations where it was the only channel. Removing
	// it would leave an empty list, which reaches every channel.
	LeftAlone []string
}

// RemoveChannelFromAll takes a channel off every registration that names it,
// leaving alone any registration it is the only channel of.
func (m *Manager) RemoveChannelFromAll(ctx context.Context, channelName string) (PruneChannelResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result PruneChannelResult
	mcps, err := m.load(ctx)
	if err != nil {
		return PruneChannelResult{}, err
	}

	changed := false
	for i := range mcps {
		remaining := slices.DeleteFunc(slices.Clone(mcps[i].Channels), func(name string) bool {
			return name == channelName
		})
		if len(remaining) == len(mcps[i].Channels) {
			continue
		}
		if len(remaining) == 0 {
			// An empty list reaches every channel, so this would widen the
			// server rather than tidy it.
			result.LeftAlone = append(result.LeftAlone, mcps[i].Name)
			continue
		}
		mcps[i].Channels = remaining
		result.Pruned = append(result.Pruned, mcps[i].Name)
		changed = true
	}
	for _, name := range result.LeftAlone {
		slog.Warn("remote mcp was scoped only to a deleted channel and still names it",
			"channel", channelName, "remote_mcp", name)
	}
	if !changed {
		return result, nil
	}
	if err := m.save(ctx, mcps); err != nil {
		return PruneChannelResult{}, err
	}
	for _, name := range result.Pruned {
		slog.Info("remote mcp no longer scoped to deleted channel",
			"channel", channelName, "remote_mcp", name)
	}
	return result, nil
}

// update applies fn to the named registration and saves the list.
func (m *Manager) update(ctx context.Context, name string, fn func(*RemoteMCP)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mcps, err := m.load(ctx)
	if err != nil {
		return err
	}
	for i := range mcps {
		if mcps[i].Name == name {
			fn(&mcps[i])
			return m.save(ctx, mcps)
		}
	}
	return fmt.Errorf("remote mcp %q not found", name)
}

func (m *Manager) RemoveRemoteMCP(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mcps, err := m.load(ctx)
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
	if err := m.save(ctx, remaining); err != nil {
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

// save writes the registrations in the current shape. Callers must hold mu.
func (m *Manager) save(ctx context.Context, mcps []RemoteMCP) error {
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
