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
	Name string // config key (e.g. "linear")
	URL  string // MCP server URL

	// BearerToken, if non-empty, sets the Authorization header.
	BearerToken string

	// ExtraHeaders are added verbatim to the request. Used for auth layers that
	// need non-Bearer credentials (e.g. Cloudflare Access service tokens send
	// CF-Access-Client-Id and CF-Access-Client-Secret). If a key collides with
	// the Authorization header set by BearerToken, BearerToken wins.
	ExtraHeaders map[string]string
}

// GenerateConfigFile writes an MCP config JSON file that Claude CLI can
// load via --mcp-config. Returns the file path.
//
// localAddr is the address of the local tclaw MCP server (e.g. "127.0.0.1:54321").
// localToken is the bearer token for authenticating with the local MCP server.
// dir is the directory to write the config file into.
// remotes are optional remote MCP servers to include alongside the local server.
func GenerateConfigFile(dir string, localAddr string, localToken string, remotes []RemoteMCPEntry) (string, error) {
	return writeConfigFile(filepath.Join(dir, "mcp-config.json"), localAddr, localToken, remotes)
}

// GenerateChannelConfigFile writes a per-channel MCP config JSON file.
// The channel config includes only the remote MCPs scoped to that channel
// (plus any globally-scoped remote MCPs). Returns the file path.
func GenerateChannelConfigFile(dir string, localAddr string, localToken string, channelName string, remotes []RemoteMCPEntry) (string, error) {
	filename := fmt.Sprintf("mcp-config-%s.json", channelName)
	return writeConfigFile(filepath.Join(dir, filename), localAddr, localToken, remotes)
}

func writeConfigFile(path string, localAddr string, localToken string, remotes []RemoteMCPEntry) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}

	localEntry := MCPServerConfig{
		Type: "http",
		URL:  fmt.Sprintf("http://%s/mcp", localAddr),
	}
	if localToken != "" {
		localEntry.Headers = map[string]string{
			"Authorization": "Bearer " + localToken,
		}
	}

	cfg := ConfigFile{
		MCPServers: map[string]MCPServerConfig{
			"tclaw": localEntry,
		},
	}

	for _, r := range remotes {
		entry := MCPServerConfig{
			Type: "http",
			URL:  r.URL,
		}
		if len(r.ExtraHeaders) > 0 {
			entry.Headers = make(map[string]string, len(r.ExtraHeaders)+1)
			for k, v := range r.ExtraHeaders {
				entry.Headers[k] = v
			}
		}
		if r.BearerToken != "" {
			if entry.Headers == nil {
				entry.Headers = make(map[string]string, 1)
			}
			entry.Headers["Authorization"] = "Bearer " + r.BearerToken
		}
		cfg.MCPServers[r.Name] = entry
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	return path, nil
}
