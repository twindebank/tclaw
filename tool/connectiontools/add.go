package connectiontools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/connection"
	"tclaw/credential"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/provider"
)

const ToolConnectionAdd = "connection_add"

func connectionAddDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolConnectionAdd,
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
				},
				"channel": {
					"type": "string",
					"description": "Channel name to scope this connection to. The provider's tools (e.g. google_*) will only be available on this channel."
				}
			},
			"required": ["provider", "label", "channel"]
		}`),
	}
}

type connectionAddArgs struct {
	Provider string `json:"provider"`
	Label    string `json:"label"`
	Channel  string `json:"channel"`
}

func connectionAddHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a connectionAddArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Provider == "" || len(a.Provider) > 64 {
			return nil, fmt.Errorf("provider is required and must be under 64 characters")
		}
		if a.Label == "" || len(a.Label) > 64 {
			return nil, fmt.Errorf("label is required and must be under 64 characters")
		}
		if a.Channel == "" {
			return nil, fmt.Errorf("channel is required — specify which channel this connection's tools should be available on")
		}

		providerID := provider.ProviderID(a.Provider)
		p := deps.Registry.Get(providerID)
		if p == nil {
			return nil, fmt.Errorf("unknown provider %q — use connection_providers to see available options", a.Provider)
		}

		conn, err := deps.Manager.Add(ctx, providerID, a.Label, a.Channel)
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

	// Bridge: create a credential set and use the new PendingFlow type.
	credSetID := credential.CredentialSetID(conn.ID)
	oauthCfg := &oauth.OAuth2Config{
		AuthURL:      p.OAuth2.AuthURL,
		TokenURL:     p.OAuth2.TokenURL,
		ClientID:     p.OAuth2.ClientID,
		ClientSecret: p.OAuth2.ClientSecret,
		Scopes:       p.OAuth2.Scopes,
		ExtraParams:  p.OAuth2.ExtraParams,
	}

	flow := &oauth.PendingFlow{
		CredSetID:   credSetID,
		OAuthConfig: oauthCfg,
		Manager:     deps.CredMgr,
		OnComplete: func() {
			if deps.OnProviderConnect != nil {
				deps.OnProviderConnect(conn.ID, deps.Manager, p)
			}
		},
	}

	state, err := deps.Callback.StartFlow(flow)
	if err != nil {
		return nil, fmt.Errorf("start oauth flow: %w", err)
	}

	authURL := oauth.BuildAuthURL(oauthCfg, state, flow.CallbackURL)

	result := map[string]any{
		"connection_id": conn.ID,
		"status":        "pending_auth",
		"auth_url":      authURL,
		"message":       fmt.Sprintf("Send this authorization URL to the user. You MUST immediately call connection_auth_wait with connection_id=%q in the same turn — do NOT end the turn without calling it.", conn.ID),
	}
	return json.Marshal(result)
}
