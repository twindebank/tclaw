package connectiontools

import (
	"context"
	"encoding/json"

	"tclaw/mcp"
	"tclaw/provider"
)

const ToolConnectionProviders = "connection_providers"

func connectionProvidersDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolConnectionProviders,
		Description: "List available service providers that can be connected (e.g. gmail, linear).",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func connectionProvidersHandler(reg *provider.Registry, deps Deps) mcp.ToolHandler {
	return func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		ids := reg.List()
		if len(ids) == 0 {
			return json.Marshal("No providers available.")
		}

		type providerInfo struct {
			ID       provider.ProviderID `json:"id"`
			Name     string              `json:"name"`
			Auth     provider.AuthType   `json:"auth_type"`
			Services []string            `json:"services,omitempty"`
		}

		var result struct {
			Providers   []providerInfo `json:"providers"`
			RedirectURL string         `json:"redirect_url,omitempty"`
		}

		for _, id := range ids {
			p := reg.Get(id)
			result.Providers = append(result.Providers, providerInfo{
				ID:       p.ID,
				Name:     p.Name,
				Auth:     p.Auth,
				Services: p.Services,
			})
		}

		// Include the OAuth redirect URL so the agent can guide users
		// through provider setup (e.g. setting redirect URIs in developer consoles).
		if deps.Callback != nil {
			result.RedirectURL = deps.Callback.CallbackURL()
		}

		return json.Marshal(result)
	}
}
