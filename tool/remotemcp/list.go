package remotemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tclaw/mcp"
)

func remoteMCPListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "remote_mcp_list",
		Description: "List all connected remote MCP servers, showing their name, URL, and auth status.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func remoteMCPListHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		mcps, err := deps.Manager.ListRemoteMCPs(ctx)
		if err != nil {
			return nil, fmt.Errorf("list remote mcps: %w", err)
		}

		type mcpInfo struct {
			Name     string `json:"name"`
			URL      string `json:"url"`
			Channel  string `json:"channel,omitempty"`
			HasAuth  bool   `json:"has_auth"`
			HasToken bool   `json:"has_token"`
		}

		result := make([]mcpInfo, 0, len(mcps))
		for _, m := range mcps {
			auth, err := deps.Manager.GetRemoteMCPAuth(ctx, m.Name)
			if err != nil {
				slog.Warn("failed to get auth for remote MCP", "name", m.Name, "err", err)
			}
			info := mcpInfo{
				Name:    m.Name,
				URL:     m.URL,
				Channel: m.Channel,
			}
			if auth != nil {
				info.HasAuth = true
				info.HasToken = auth.AccessToken != ""
			}
			result = append(result, info)
		}

		return json.Marshal(result)
	}
}
