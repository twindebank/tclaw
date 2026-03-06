package connectiontools

import (
	"context"
	"encoding/json"

	"tclaw/mcp"
	"tclaw/provider"
)

func connectionProvidersDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "connection_providers",
		Description: "List available service providers that can be connected (e.g. gmail, linear).",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func connectionProvidersHandler(reg *provider.Registry) mcp.ToolHandler {
	return func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		ids := reg.List()
		if len(ids) == 0 {
			return json.Marshal("No providers available.")
		}

		type providerInfo struct {
			ID   provider.ProviderID `json:"id"`
			Name string              `json:"name"`
			Auth provider.AuthType   `json:"auth_type"`
		}

		var result []providerInfo
		for _, id := range ids {
			p := reg.Get(id)
			result = append(result, providerInfo{
				ID:   p.ID,
				Name: p.Name,
				Auth: p.Auth,
			})
		}

		return json.Marshal(result)
	}
}
