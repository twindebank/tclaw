package remotemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tclaw/internal/mcp"
)

const ToolRemoteMCPList = "remote_mcp_list"

func remoteMCPListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolRemoteMCPList,
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

		// URL-handling contract matches remote_mcp_add: host always, full url
		// only when URLSensitive is false. Agents shouldn't receive sensitive
		// URLs via tool output — url_is_secret makes that state explicit.
		result := make([]map[string]any, 0, len(mcps))
		for _, m := range mcps {
			auth, authErr := deps.Manager.GetRemoteMCPAuth(ctx, m.Name)
			if authErr != nil {
				slog.Warn("failed to get auth for remote MCP", "name", m.Name, "err", authErr)
			}
			info := urlResponseFields(m.URL, m.URLSensitive)
			info["name"] = m.Name
			if m.Channel != "" {
				info["channel"] = m.Channel
			}
			info["has_auth"] = auth != nil
			info["has_token"] = auth != nil && auth.AccessToken != ""
			result = append(result, info)
		}

		return json.Marshal(result)
	}
}
