package remotemcp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/mcp"
	"tclaw/internal/remotemcpstore"
)

// TestAddToConfigFile covers the full path from remote_mcp_add tool call
// through to the generated --mcp-config JSON file on disk. This is the
// contract the router relies on: static headers stored via the tool must
// surface in the generated config so the Claude CLI sends them.
func TestAddToConfigFile(t *testing.T) {
	t.Run("static headers land in config file", func(t *testing.T) {
		h, mgr, _ := setup(t)

		_ = callTool(t, h, "remote_mcp_add", map[string]any{
			"name":                "home-assistant",
			"url":                 "https://ha-mcp.example.com/secret_path_abc",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers": map[string]string{
				"CF-Access-Client-Id":     "client-id.access",
				"CF-Access-Client-Secret": "super-secret",
			},
		})

		cfg := generateConfigFromManager(t, mgr)

		got, ok := cfg.MCPServers["home-assistant"]
		require.True(t, ok, "home-assistant entry missing from config")
		require.Equal(t, "https://ha-mcp.example.com/secret_path_abc", got.URL)
		require.Equal(t, "client-id.access", got.Headers["CF-Access-Client-Id"])
		require.Equal(t, "super-secret", got.Headers["CF-Access-Client-Secret"])
		require.NotContains(t, got.Headers, "Authorization", "no OAuth in this flow")
	})

	t.Run("remove drops the server from the config", func(t *testing.T) {
		h, mgr, _ := setup(t)

		_ = callTool(t, h, "remote_mcp_add", map[string]any{
			"name":                "home-assistant",
			"url":                 "https://ha-mcp.example.com/secret",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers":             map[string]string{"CF-Access-Client-Id": "id"},
		})

		cfg := generateConfigFromManager(t, mgr)
		require.Contains(t, cfg.MCPServers, "home-assistant")

		_ = callTool(t, h, "remote_mcp_remove", map[string]any{
			"name": "home-assistant",
		})

		cfg = generateConfigFromManager(t, mgr)
		require.NotContains(t, cfg.MCPServers, "home-assistant", "removed MCP should not appear in config")
	})
}

// TestHeadersOnWire proves that the credentials in the generated config are
// faithfully transmitted on the HTTP request. This is the last contract
// before the Claude CLI takes over — if this test passes, any bug from here
// on is in the CLI, not in tclaw.
func TestHeadersOnWire(t *testing.T) {
	t.Run("static headers reach the remote server intact", func(t *testing.T) {
		// Capture inbound headers on a fake remote MCP server. Must be TLS
		// because remote_mcp_add requires https URLs.
		var received http.Header
		var receivedBody []byte
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			received = r.Header.Clone()
			body, _ := io.ReadAll(r.Body)
			receivedBody = body
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		}))
		t.Cleanup(server.Close)

		h, mgr, _ := setup(t)

		_ = callTool(t, h, "remote_mcp_add", map[string]any{
			"name":                "ha-wire",
			"url":                 server.URL + "/mcp_secret_path",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers": map[string]string{
				"CF-Access-Client-Id":     "client-id.access",
				"CF-Access-Client-Secret": "wire-test-secret",
			},
		})

		cfg := generateConfigFromManager(t, mgr)
		entry, ok := cfg.MCPServers["ha-wire"]
		require.True(t, ok)

		// Simulate what the CLI does: issue a POST with the configured headers.
		req, err := http.NewRequest(http.MethodPost, entry.URL, nil)
		require.NoError(t, err)
		for k, v := range entry.Headers {
			req.Header.Set(k, v)
		}
		// server.Client() trusts the httptest self-signed cert.
		resp, err := server.Client().Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		require.Equal(t, http.StatusOK, resp.StatusCode)

		require.Equal(t, "client-id.access", received.Get("CF-Access-Client-Id"))
		require.Equal(t, "wire-test-secret", received.Get("CF-Access-Client-Secret"))
		require.Empty(t, receivedBody, "sanity: empty body POST")
	})
}

// --- helpers ---

// generateConfigFromManager replicates the router's buildRemoteMCPEntries
// closure inline so the test doesn't need to start a full router. If the
// router's logic diverges, these tests must be kept in sync.
func generateConfigFromManager(t *testing.T, mgr *remotemcpstore.Manager) mcp.ConfigFile {
	t.Helper()
	ctx := context.Background()

	mcps, err := mgr.ListRemoteMCPs(ctx)
	require.NoError(t, err)

	var entries []mcp.RemoteMCPEntry
	for _, m := range mcps {
		entry := mcp.RemoteMCPEntry{Name: m.Name, URL: m.URL}
		auth, err := mgr.GetRemoteMCPAuth(ctx, m.Name)
		require.NoError(t, err)
		if auth != nil {
			if auth.AccessToken != "" {
				entry.BearerToken = auth.AccessToken
			}
			if len(auth.StaticHeaders) > 0 {
				entry.ExtraHeaders = auth.StaticHeaders
			}
		}
		entries = append(entries, entry)
	}

	dir := t.TempDir()
	path, err := mcp.GenerateConfigFile(dir, "127.0.0.1:9999", "local-tok", entries)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "mcp-config.json"), path)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var cfg mcp.ConfigFile
	require.NoError(t, json.Unmarshal(raw, &cfg))
	return cfg
}
