package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MCPServerConfig is a single entry in the --mcp-config JSON file.
type MCPServerConfig struct {
	Type    string            `json:"type,omitempty"` // "http" or "stdio"
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ConfigFile is the top-level structure for --mcp-config JSON.
type ConfigFile struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// RemoteMCPEntry describes a remote MCP server to include in the config file.
type RemoteMCPEntry struct {
	Name        string // config key (e.g. "linear")
	URL         string // MCP server URL
	BearerToken string // optional — if non-empty, adds Authorization header
}

// GenerateConfigFile writes an MCP config JSON file that Claude CLI can
// load via --mcp-config. Returns the file path.
//
// localAddr is the address of the local tclaw MCP server (e.g. "127.0.0.1:54321").
// dir is the directory to write the config file into.
// remotes are optional remote MCP servers to include alongside the local server.
func GenerateConfigFile(dir string, localAddr string, remotes []RemoteMCPEntry) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}

	cfg := ConfigFile{
		MCPServers: map[string]MCPServerConfig{
			"tclaw": {
				Type: "http",
				URL:  fmt.Sprintf("http://%s/mcp", localAddr),
			},
		},
	}

	for _, r := range remotes {
		entry := MCPServerConfig{
			Type: "http",
			URL:  r.URL,
		}
		if r.BearerToken != "" {
			entry.Headers = map[string]string{
				"Authorization": "Bearer " + r.BearerToken,
			}
		}
		cfg.MCPServers[r.Name] = entry
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}

	path := filepath.Join(dir, "mcp-config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	return path, nil
}
