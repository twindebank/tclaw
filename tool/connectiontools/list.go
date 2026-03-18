package connectiontools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/provider"
)

func connectionListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "connection_list",
		Description: "List all connections to external services, showing their provider, label, and whether credentials are configured.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func connectionListHandler(mgr *connection.Manager) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		conns, err := mgr.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("list connections: %w", err)
		}
		type connInfo struct {
			ID       connection.ConnectionID `json:"id"`
			Provider provider.ProviderID     `json:"provider"`
			Label    string                  `json:"label"`
			Channel  string                  `json:"channel,omitempty"`
			HasCreds bool                    `json:"has_credentials"`
		}

		result := make([]connInfo, 0, len(conns))
		for _, c := range conns {
			info := connInfo{
				ID:       c.ID,
				Provider: c.ProviderID,
				Label:    c.Label,
				Channel:  c.Channel,
			}
			creds, err := mgr.GetCredentials(ctx, c.ID)
			if err != nil {
				// Log but continue — one broken credential shouldn't hide all connections.
				slog.Warn("failed to read credentials for connection", "id", c.ID, "err", err)
			} else {
				info.HasCreds = creds != nil && creds.AccessToken != ""
			}
			result = append(result, info)
		}

		return json.Marshal(result)
	}
}
