package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateConfigFile_RemoteEntries(t *testing.T) {
	t.Run("proxy URL and headers are written verbatim, no upstream credentials", func(t *testing.T) {
		cfg := writeAndRead(t, []RemoteMCPEntry{
			{
				Name:    "linear",
				URL:     "http://127.0.0.1:54321/linear",
				Headers: map[string]string{"X-Tclaw-Proxy-Token": "proxy-tok"},
			},
		})
		got := cfg.MCPServers["linear"]
		require.Equal(t, "http://127.0.0.1:54321/linear", got.URL)
		require.Equal(t, "http", got.Type)
		require.Equal(t, "proxy-tok", got.Headers["X-Tclaw-Proxy-Token"])
		require.Len(t, got.Headers, 1)
		require.NotContains(t, got.Headers, "Authorization", "no upstream auth belongs in the config")
	})

	t.Run("no headers means no headers", func(t *testing.T) {
		cfg := writeAndRead(t, []RemoteMCPEntry{
			{Name: "open", URL: "http://127.0.0.1:54321/open"},
		})
		require.Empty(t, cfg.MCPServers["open"].Headers)
	})

	t.Run("the local tclaw server is always present with its bearer token", func(t *testing.T) {
		cfg := writeAndRead(t, nil)
		local := cfg.MCPServers["tclaw"]
		require.Equal(t, "http://127.0.0.1:9999/mcp", local.URL)
		require.Equal(t, "Bearer local-tok", local.Headers["Authorization"])
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
