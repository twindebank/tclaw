package connectiontools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/provider"
)

func connectionAddDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "connection_add",
		Description: "Add a new connection to an external service. Specify the provider (e.g. 'gmail') and a label (e.g. 'work', 'personal'). For OAuth providers, returns an authorization URL the user must visit. The tool blocks until the user completes authorization (up to 5 minutes).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"provider": {
					"type": "string",
					"description": "Provider ID (e.g. 'gmail'). Use connection_providers to see available options."
				},
				"label": {
					"type": "string",
					"description": "Label to identify this connection (e.g. 'work', 'personal'). Must be unique per provider."
				}
			},
			"required": ["provider", "label"]
		}`),
	}
}

type connectionAddArgs struct {
	Provider string `json:"provider"`
	Label    string `json:"label"`
}

func connectionAddHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a connectionAddArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		providerID := provider.ProviderID(a.Provider)
		p := deps.Registry.Get(providerID)
		if p == nil {
			return nil, fmt.Errorf("unknown provider %q — use connection_providers to see available options", a.Provider)
		}

		conn, err := deps.Manager.Add(ctx, providerID, a.Label)
		if err != nil {
			return nil, fmt.Errorf("add connection: %w", err)
		}

		switch p.Auth {
		case provider.AuthNone:
			result := map[string]any{
				"connection_id": conn.ID,
				"status":        "ready",
				"message":       fmt.Sprintf("Connection %s created. No authentication required.", conn.ID),
			}
			return json.Marshal(result)

		case provider.AuthOAuth2:
			return handleOAuthAdd(ctx, deps, p, conn)

		case provider.AuthAPIKey:
			result := map[string]any{
				"connection_id": conn.ID,
				"status":        "pending_auth",
				"message":       fmt.Sprintf("Connection %s created. API key configuration not yet implemented.", conn.ID),
			}
			return json.Marshal(result)

		default:
			return nil, fmt.Errorf("unsupported auth type %q for provider %s", p.Auth, p.ID)
		}
	}
}

func handleOAuthAdd(ctx context.Context, deps Deps, p *provider.Provider, conn *connection.Connection) (json.RawMessage, error) {
	if deps.Callback == nil {
		return nil, fmt.Errorf("OAuth is not configured — set providers.%s.client_id and client_secret in tclaw.yaml", p.ID)
	}

	flow := &oauth.PendingFlow{
		ConnID:   conn.ID,
		Provider: p,
		Manager:  deps.Manager,
		OnConnect: func() {
			if deps.OnProviderConnect != nil {
				deps.OnProviderConnect(conn.ID, deps.Manager, p)
			}
		},
	}

	state, err := deps.Callback.StartFlow(flow)
	if err != nil {
		return nil, fmt.Errorf("start oauth flow: %w", err)
	}

	authURL := oauth.BuildAuthURL(p.OAuth2, state, flow.CallbackURL)

	result := map[string]any{
		"connection_id": conn.ID,
		"status":        "pending_auth",
		"auth_url":      authURL,
		"message":       fmt.Sprintf("Send this authorization URL to the user. After they click it and authorize, use connection_auth_wait with connection_id=%q to confirm completion.", conn.ID),
	}
	return json.Marshal(result)
}
