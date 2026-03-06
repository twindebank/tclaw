package connectiontools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/provider"
)

// Deps holds the dependencies for connection management tools.
type Deps struct {
	Manager  *connection.Manager
	Registry *provider.Registry
	Callback *oauth.CallbackServer // nil if OAuth is not configured
	Handler  *mcp.Handler          // MCP handler for dynamic tool registration

	// OnGmailConnect is called when a Gmail OAuth flow completes so the
	// caller can register Gmail tools dynamically. Avoids importing the
	// gmail sub-package from here.
	OnGmailConnect func(connID connection.ConnectionID, mgr *connection.Manager, p *provider.Provider)
}

// RegisterTools adds the connection management tools to the MCP handler.
// These are always available regardless of which providers are connected.
func RegisterTools(h *mcp.Handler, deps Deps) {
	h.Register(connectionListDef(), connectionListHandler(deps.Manager))
	h.Register(connectionProvidersDef(), connectionProvidersHandler(deps.Registry))
	h.Register(connectionAddDef(), connectionAddHandler(deps))
	h.Register(connectionRemoveDef(), connectionRemoveHandler(deps.Manager))
}

// RegisterAuthWaitTool adds the connection_auth_wait tool. Separated from
// RegisterTools because it's only useful when OAuth is configured.
func RegisterAuthWaitTool(h *mcp.Handler, mgr *connection.Manager) {
	h.Register(connectionAuthWaitDef(), connectionAuthWaitHandler(mgr))
}

// --- connection_list ---

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

// --- connection_providers ---

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

// --- connection_add ---

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
			if deps.OnGmailConnect != nil && p.ID == provider.GmailProviderID {
				deps.OnGmailConnect(conn.ID, deps.Manager, p)
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

// --- connection_remove ---

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

// --- connection_auth_wait ---

const authWaitTimeout = 5 * time.Minute

func connectionAuthWaitDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "connection_auth_wait",
		Description: "Wait for a pending OAuth authorization to complete. Call this after sending the auth URL to the user. Blocks until the user finishes authorizing (up to 5 minutes) or checks if credentials are already stored.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"connection_id": {
					"type": "string",
					"description": "The connection ID to wait for (e.g. 'gmail/work')."
				}
			},
			"required": ["connection_id"]
		}`),
	}
}

type connectionAuthWaitArgs struct {
	ConnectionID string `json:"connection_id"`
}

func connectionAuthWaitHandler(mgr *connection.Manager) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a connectionAuthWaitArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		connID := connection.ConnectionID(a.ConnectionID)

		// Check if credentials already exist (callback already fired).
		creds, err := mgr.GetCredentials(ctx, connID)
		if err != nil {
			return nil, fmt.Errorf("check credentials: %w", err)
		}
		if creds != nil && creds.AccessToken != "" {
			result := map[string]string{
				"connection_id": string(connID),
				"status":        "authorized",
				"message":       fmt.Sprintf("Connection %s is authorized and ready to use.", connID),
			}
			return json.Marshal(result)
		}

		// Poll for credentials until timeout or ctx cancellation.
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		deadline := time.After(authWaitTimeout)

		for {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("authorization wait cancelled")

			case <-deadline:
				result := map[string]string{
					"connection_id": string(connID),
					"status":        "timeout",
					"message":       fmt.Sprintf("Authorization timed out after %s. The user may not have completed the OAuth flow. They can try again with connection_add.", authWaitTimeout),
				}
				return json.Marshal(result)

			case <-ticker.C:
				creds, err := mgr.GetCredentials(ctx, connID)
				if err != nil {
					return nil, fmt.Errorf("check credentials: %w", err)
				}
				if creds != nil && creds.AccessToken != "" {
					result := map[string]string{
						"connection_id": string(connID),
						"status":        "authorized",
						"message":       fmt.Sprintf("Connection %s is now authorized and ready to use!", connID),
					}
					return json.Marshal(result)
				}
			}
		}
	}
}
