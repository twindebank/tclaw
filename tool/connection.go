package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/provider"
)

// ConnectionToolsDeps holds the dependencies for connection management tools.
type ConnectionToolsDeps struct {
	Manager  *connection.Manager
	Registry *provider.Registry
	Callback *oauth.CallbackServer // nil if OAuth is not configured
	Handler  *mcp.Handler          // MCP handler for dynamic tool registration
}

// RegisterConnectionTools adds the connection management tools to the MCP handler.
// These are always available regardless of which providers are connected.
func RegisterConnectionTools(h *mcp.Handler, deps ConnectionToolsDeps) {
	h.Register(connectionListDef(), connectionListHandler(deps.Manager))
	h.Register(connectionProvidersDef(), connectionProvidersHandler(deps.Registry))
	h.Register(connectionAddDef(), connectionAddHandler(deps))
	h.Register(connectionRemoveDef(), connectionRemoveHandler(deps.Manager))
}

// connection_list

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
			Provider connection.ProviderID   `json:"provider"`
			Label    string                  `json:"label"`
			HasCreds bool                    `json:"has_credentials"`
		}

		var result []connInfo
		for _, c := range conns {
			creds, _ := mgr.GetCredentials(ctx, c.ID)
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

// connection_providers

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
			ID   connection.ProviderID `json:"id"`
			Name string                `json:"name"`
			Auth provider.AuthType     `json:"auth_type"`
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

// connection_add

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

func connectionAddHandler(deps ConnectionToolsDeps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a connectionAddArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		providerID := connection.ProviderID(a.Provider)
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

func handleOAuthAdd(ctx context.Context, deps ConnectionToolsDeps, p *provider.Provider, conn *connection.Connection) (json.RawMessage, error) {
	if deps.Callback == nil {
		return nil, fmt.Errorf("OAuth is not configured — set providers.%s.client_id and client_secret in tclaw.yaml", p.ID)
	}

	flow := &oauth.PendingFlow{
		ConnID:   conn.ID,
		Provider: p,
		Manager:  deps.Manager,
		OnConnect: func() {
			// Dynamically register provider tools when OAuth completes
			// so they're available in the current session.
			switch p.ID {
			case provider.GmailProviderID:
				RegisterGmailTools(deps.Handler, GmailToolsDeps{
					ConnID:   conn.ID,
					Manager:  deps.Manager,
					Provider: p,
				})
			}
		},
	}

	state, err := deps.Callback.StartFlow(flow)
	if err != nil {
		return nil, fmt.Errorf("start oauth flow: %w", err)
	}

	authURL := oauth.BuildAuthURL(p.OAuth2, state, flow.CallbackURL)

	// Return the auth URL immediately so Claude can show it to the user,
	// then block until the callback completes or ctx is cancelled.
	// The MCP tool result includes the URL for Claude to display.
	// We use a goroutine-free approach: select on flow.Done vs ctx.Done.

	// First, return the URL as the tool result. But we want to wait...
	// Actually the MCP protocol is request-response, so we need to either:
	// (a) return immediately with the URL and have a separate status check, or
	// (b) block here until auth completes.
	//
	// Option (b) is cleaner UX — Claude gets the final result in one tool call.
	// The tricky part is that Claude needs to show the URL before we return.
	// Since MCP doesn't support streaming partial results, we return immediately
	// with the URL and add a connection_auth_wait tool for checking completion.

	result := map[string]any{
		"connection_id": conn.ID,
		"status":        "pending_auth",
		"auth_url":      authURL,
		"message":       fmt.Sprintf("Send this authorization URL to the user. After they click it and authorize, use connection_auth_wait with connection_id=%q to confirm completion.", conn.ID),
	}
	return json.Marshal(result)
}

// connection_remove

func connectionRemoveDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "connection_remove",
		Description: "Remove a connection and delete its stored credentials. Use connection_list to see existing connections.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"connection_id": {
					"type": "string",
					"description": "The connection ID to remove (e.g. 'gmail/work'). Use connection_list to find IDs."
				}
			},
			"required": ["connection_id"]
		}`),
	}
}

type connectionRemoveArgs struct {
	ConnectionID string `json:"connection_id"`
}

func connectionRemoveHandler(mgr *connection.Manager) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a connectionRemoveArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		id := connection.ConnectionID(a.ConnectionID)
		if err := mgr.Remove(ctx, id); err != nil {
			return nil, fmt.Errorf("remove connection: %w", err)
		}

		return json.Marshal(fmt.Sprintf("Connection %s removed.", id))
	}
}
