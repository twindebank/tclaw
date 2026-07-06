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
	"tclaw/internal/remotemcpproxy"
	"tclaw/internal/remotemcpstore"
)

// TestBuildMCPConfigPaths guards that per-channel config files route remote MCPs
// through the auth proxy and never embed upstream credentials — the proxy
// injects those server-side, keeping them out of the sandbox-readable config.
func TestBuildMCPConfigPaths_RoutesThroughProxy(t *testing.T) {
	t.Run("channel-scoped server points at the proxy with no upstream secrets", func(t *testing.T) {
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

		proxy := startTestProxy(t, mgr)

		chID := channel.ChannelID("telegram:homeassistant")
		chMap := map[channel.ChannelID]channel.Channel{
			chID: &stubNamedChannel{id: chID, name: "homeassistant"},
		}

		mcpConfigDir := t.TempDir()
		paths := buildMCPConfigPaths(ctx, chMap, mgr, proxy, proxy.Token(), mcpConfigDir, "127.0.0.1:1", "local-token")

		path, ok := paths[chID]
		require.True(t, ok, "expected a per-channel config path for the homeassistant channel")

		cfg := readMCPConfigFile(t, path)
		entry, ok := cfg.MCPServers["home-assistant"]
		require.True(t, ok, "home-assistant entry missing from per-channel config")
		require.Equal(t, proxy.RemoteURL("home-assistant"), entry.URL, "config must dial the proxy, not the upstream")
		require.Equal(t, proxy.Token(), entry.Headers[remotemcpproxy.ProxyTokenHeader])
		require.NotContains(t, entry.Headers, "CF-Access-Client-Id")
		require.NotContains(t, entry.Headers, "CF-Access-Client-Secret")
		require.NotContains(t, entry.Headers, "Authorization")

		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "cf-client-secret", "no upstream secret may reach the config file")
		require.NotContains(t, string(raw), "ha-mcp.example.com", "no upstream URL may reach the config file")
	})
}

// --- helpers ---

func startTestProxy(t *testing.T, mgr *remotemcpstore.Manager) *remotemcpproxy.Server {
	t.Helper()
	srv, err := remotemcpproxy.NewServer(remotemcpproxy.Config{Store: mgr})
	require.NoError(t, err)
	_, err = srv.Start("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	return srv
}

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
