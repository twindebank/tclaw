package router

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/remotemcpstore"
)

// TestBuildMCPConfigPaths guards the wiring that propagates auth material from
// the remote MCP store into per-channel config files. Bearer tokens and static
// headers (e.g. Cloudflare Access client id / secret) must both reach the CLI
// or calls to the remote server fail at the auth layer with no useful signal.
func TestBuildMCPConfigPaths_PropagatesAuth(t *testing.T) {
	t.Run("static headers are written to the per-channel config", func(t *testing.T) {
		ctx := context.Background()
		mgr := newTestRemoteMCPManager(t)

		_, err := mgr.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{
			Name:    "home-assistant",
			URL:     "https://ha-mcp.example.com/private_path",
			Channel: "homeassistant",
		})
		require.NoError(t, err)
		require.NoError(t, mgr.SetRemoteMCPAuth(ctx, "home-assistant", &remotemcpstore.RemoteMCPAuth{
			StaticHeaders: map[string]string{
				"CF-Access-Client-Id":     "cf-client-id",
				"CF-Access-Client-Secret": "cf-client-secret",
			},
		}))

		chID := channel.ChannelID("telegram:homeassistant")
		chMap := map[channel.ChannelID]channel.Channel{
			chID: &stubNamedChannel{id: chID, name: "homeassistant"},
		}

		mcpConfigDir := t.TempDir()
		paths := buildMCPConfigPaths(ctx, chMap, mgr, mcpConfigDir, "127.0.0.1:1", "local-token")

		path, ok := paths[chID]
		require.True(t, ok, "expected a per-channel config path for the homeassistant channel")

		cfg := readMCPConfigFile(t, path)
		entry, ok := cfg.MCPServers["home-assistant"]
		require.True(t, ok, "home-assistant entry missing from per-channel config")
		require.Equal(t, "cf-client-id", entry.Headers["CF-Access-Client-Id"])
		require.Equal(t, "cf-client-secret", entry.Headers["CF-Access-Client-Secret"])
	})

	t.Run("bearer token and static headers coexist", func(t *testing.T) {
		ctx := context.Background()
		mgr := newTestRemoteMCPManager(t)

		_, err := mgr.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{
			Name:    "hybrid",
			URL:     "https://hybrid.example.com/mcp",
			Channel: "homeassistant",
		})
		require.NoError(t, err)
		require.NoError(t, mgr.SetRemoteMCPAuth(ctx, "hybrid", &remotemcpstore.RemoteMCPAuth{
			AccessToken:   "oauth-token",
			StaticHeaders: map[string]string{"X-Tenant": "acme"},
		}))

		chID := channel.ChannelID("telegram:homeassistant")
		chMap := map[channel.ChannelID]channel.Channel{
			chID: &stubNamedChannel{id: chID, name: "homeassistant"},
		}

		paths := buildMCPConfigPaths(ctx, chMap, mgr, t.TempDir(), "127.0.0.1:1", "local-token")
		cfg := readMCPConfigFile(t, paths[chID])
		entry := cfg.MCPServers["hybrid"]
		require.Equal(t, "Bearer oauth-token", entry.Headers["Authorization"])
		require.Equal(t, "acme", entry.Headers["X-Tenant"])
	})
}

// --- helpers ---

func newTestRemoteMCPManager(t *testing.T) *remotemcpstore.Manager {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return remotemcpstore.NewManager(s, &testMemorySecretStore{data: map[string]string{}})
}

func readMCPConfigFile(t *testing.T, path string) mcp.ConfigFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg mcp.ConfigFile
	require.NoError(t, json.Unmarshal(raw, &cfg))
	return cfg
}

type testMemorySecretStore struct {
	data map[string]string
}

var _ secret.Store = (*testMemorySecretStore)(nil)

func (m *testMemorySecretStore) Get(_ context.Context, key string) (string, error) {
	return m.data[key], nil
}

func (m *testMemorySecretStore) Set(_ context.Context, key, value string) error {
	m.data[key] = value
	return nil
}

func (m *testMemorySecretStore) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}
