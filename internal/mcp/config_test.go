package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateConfigFile_RemoteHeaders(t *testing.T) {
	t.Run("bearer token only", func(t *testing.T) {
		cfg := writeAndRead(t, []RemoteMCPEntry{
			{Name: "linear", URL: "https://mcp.linear.app/sse", BearerToken: "tok"},
		})
		got := cfg.MCPServers["linear"].Headers
		require.Equal(t, "Bearer tok", got["Authorization"])
		require.Len(t, got, 1)
	})

	t.Run("extra headers only", func(t *testing.T) {
		cfg := writeAndRead(t, []RemoteMCPEntry{
			{
				Name: "ha-mcp",
				URL:  "https://ha-mcp.example.com/mcp_abc",
				ExtraHeaders: map[string]string{
					"CF-Access-Client-Id":     "client-id",
					"CF-Access-Client-Secret": "client-secret",
				},
			},
		})
		got := cfg.MCPServers["ha-mcp"].Headers
		require.Equal(t, "client-id", got["CF-Access-Client-Id"])
		require.Equal(t, "client-secret", got["CF-Access-Client-Secret"])
		require.NotContains(t, got, "Authorization")
	})

	t.Run("bearer plus extra headers", func(t *testing.T) {
		cfg := writeAndRead(t, []RemoteMCPEntry{
			{
				Name:        "combo",
				URL:         "https://combo.example.com/mcp",
				BearerToken: "tok",
				ExtraHeaders: map[string]string{
					"X-Tenant": "acme",
				},
			},
		})
		got := cfg.MCPServers["combo"].Headers
		require.Equal(t, "Bearer tok", got["Authorization"])
		require.Equal(t, "acme", got["X-Tenant"])
		require.Len(t, got, 2)
	})

	t.Run("bearer wins when extra headers collide on Authorization", func(t *testing.T) {
		cfg := writeAndRead(t, []RemoteMCPEntry{
			{
				Name:        "collide",
				URL:         "https://collide.example.com/mcp",
				BearerToken: "real-bearer",
				ExtraHeaders: map[string]string{
					"Authorization": "Basic fake",
				},
			},
		})
		require.Equal(t, "Bearer real-bearer", cfg.MCPServers["collide"].Headers["Authorization"])
	})

	t.Run("no auth at all", func(t *testing.T) {
		cfg := writeAndRead(t, []RemoteMCPEntry{
			{Name: "open", URL: "https://open.example.com/mcp"},
		})
		require.Empty(t, cfg.MCPServers["open"].Headers)
	})
}

// --- helpers ---

func writeAndRead(t *testing.T, remotes []RemoteMCPEntry) ConfigFile {
	t.Helper()
	dir := t.TempDir()
	path, err := GenerateConfigFile(dir, "127.0.0.1:9999", "local-tok", remotes)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "mcp-config.json"), path)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var cfg ConfigFile
	require.NoError(t, json.Unmarshal(raw, &cfg))
	return cfg
}
