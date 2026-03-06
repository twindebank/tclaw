package connectiontools

import (
	"context"
	"encoding/json"
	"fmt"

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
		if len(conns) == 0 {
			return json.Marshal("No connections configured. Use connection_add to connect a service.")
		}

		type connInfo struct {
			ID       connection.ConnectionID `json:"id"`
			Provider provider.ProviderID     `json:"provider"`
			Label    string                  `json:"label"`
			HasCreds bool                    `json:"has_credentials"`
		}

		var result []connInfo
		for _, c := range conns {
			creds, err := mgr.GetCredentials(ctx, c.ID)
			if err != nil {
				return nil, fmt.Errorf("get credentials for %s: %w", c.ID, err)
			}
			result = append(result, connInfo{
				ID:       c.ID,
				Provider: c.ProviderID,
				Label:    c.Label,
				HasCreds: creds != nil && creds.AccessToken != "",
			})
		}

		return json.Marshal(result)
	}
}
