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

// TestBuildMCPConfigPaths_Scoping guards which servers reach which channel. A
// channel-scoped server must not be attached to every channel — each one costs
// a handshake, or a cold start, on every turn of every channel it reaches.
func TestBuildMCPConfigPaths_Scoping(t *testing.T) {
	t.Run("a channel gets the global servers plus its own, and no other channel's", func(t *testing.T) {
		ctx := context.Background()
		mgr := newTestRemoteMCPManager(t)

		for _, params := range []remotemcpstore.AddRemoteMCPParams{
			{Name: "shared", URL: "https://shared.example.com"},
			{Name: "browser", URL: "https://browser.example.com", Channel: "admin"},
			{Name: "house", URL: "https://house.example.com", Channel: "homeassistant"},
		} {
			_, err := mgr.AddRemoteMCP(ctx, params)
			require.NoError(t, err)
		}

		proxy := startTestProxy(t, mgr)

		adminID := channel.ChannelID("telegram:admin")
		emailID := channel.ChannelID("telegram:email")
		chMap := map[channel.ChannelID]channel.Channel{
			adminID: &stubNamedChannel{id: adminID, name: "admin"},
			emailID: &stubNamedChannel{id: emailID, name: "email"},
		}

		paths := buildMCPConfigPaths(ctx, chMap, mgr, proxy, proxy.Token(), t.TempDir(), "127.0.0.1:1", "local-token")

		adminPath, ok := paths[adminID]
		require.True(t, ok, "admin has a scoped server so it needs its own config")
		admin := readMCPConfigFile(t, adminPath)
		require.Contains(t, admin.MCPServers, "browser", "admin must keep the server scoped to it")
		require.Contains(t, admin.MCPServers, "shared", "a scoped channel must still get the global servers")
		require.NotContains(t, admin.MCPServers, "house", "another channel's server must not leak in")

		require.NotContains(t, paths, emailID,
			"a channel with no scoped servers needs no file — the default config carries the global ones")
	})

	t.Run("partitions global and channel-scoped servers", func(t *testing.T) {
		global, scoped := partitionRemoteMCPs([]remotemcpstore.RemoteMCP{
			{Name: "shared"},
			{Name: "browser", Channel: "admin"},
			{Name: "house", Channel: "homeassistant"},
			{Name: "notes", Channel: "admin"},
		})

		require.Len(t, global, 1)
		require.Equal(t, "shared", global[0].Name)
		require.Equal(t, []string{"browser", "notes"}, remoteMCPNames(scoped["admin"]))
		require.Equal(t, []string{"house"}, remoteMCPNames(scoped["homeassistant"]))
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
