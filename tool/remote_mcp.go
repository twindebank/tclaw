package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/oauth"
)

// RemoteMCPToolsDeps holds dependencies for remote MCP management tools.
type RemoteMCPToolsDeps struct {
	Manager  *connection.Manager
	Callback *oauth.CallbackServer // nil if OAuth callback is not configured

	// ConfigUpdater is called after a remote MCP is added or removed to
	// regenerate the MCP config file. The next Claude turn picks up the change.
	ConfigUpdater func(ctx context.Context) error
}

// RegisterRemoteMCPTools adds the remote MCP management tools to the MCP handler.
func RegisterRemoteMCPTools(h *mcp.Handler, deps RemoteMCPToolsDeps) {
	h.Register(remoteMCPListDef(), remoteMCPListHandler(deps))
	h.Register(remoteMCPAddDef(), remoteMCPAddHandler(deps))
	h.Register(remoteMCPRemoveDef(), remoteMCPRemoveHandler(deps))
}

// --- remote_mcp_list ---

func remoteMCPListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "remote_mcp_list",
		Description: "List all connected remote MCP servers, showing their name, URL, and auth status.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func remoteMCPListHandler(deps RemoteMCPToolsDeps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		mcps, err := deps.Manager.ListRemoteMCPs(ctx)
		if err != nil {
			return nil, fmt.Errorf("list remote mcps: %w", err)
		}
		if len(mcps) == 0 {
			return json.Marshal("No remote MCP servers connected. Use remote_mcp_add to connect one.")
		}

		type mcpInfo struct {
			Name     string `json:"name"`
			URL      string `json:"url"`
			HasAuth  bool   `json:"has_auth"`
			HasToken bool   `json:"has_token"`
		}

		var result []mcpInfo
		for _, m := range mcps {
			auth, _ := deps.Manager.GetRemoteMCPAuth(ctx, m.Name)
			info := mcpInfo{
				Name: m.Name,
				URL:  m.URL,
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

// --- remote_mcp_add ---

func remoteMCPAddDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "remote_mcp_add",
		Description: "Connect a remote MCP server by URL. Discovers OAuth requirements automatically. If OAuth is needed, returns an authorization URL the user must visit. After auth completes, the remote MCP's tools will be available on the next message turn.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"url": {
					"type": "string",
					"description": "The MCP server URL (e.g. 'https://mcp.linear.app/sse')."
				},
				"name": {
					"type": "string",
					"description": "A short label for this server (e.g. 'linear', 'notion'). Used as the MCP server name in tool prefixes."
				}
			},
			"required": ["url", "name"]
		}`),
	}
}

type remoteMCPAddArgs struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

func remoteMCPAddHandler(deps RemoteMCPToolsDeps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a remoteMCPAddArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		// Store the remote MCP entry.
		entry, err := deps.Manager.AddRemoteMCP(ctx, a.Name, a.URL)
		if err != nil {
			return nil, fmt.Errorf("add remote mcp: %w", err)
		}

		slog.Info("discovering auth for remote MCP", "name", a.Name, "url", a.URL)

		// Discover whether OAuth is required.
		authMeta, err := mcp.DiscoverAuth(ctx, a.URL)
		if err != nil {
			slog.Warn("auth discovery failed, adding without auth", "name", a.Name, "err", err)
			if updateErr := deps.ConfigUpdater(ctx); updateErr != nil {
				slog.Error("failed to update mcp config", "err", updateErr)
			}
			result := map[string]any{
				"name":    entry.Name,
				"url":     entry.URL,
				"status":  "ready",
				"message": fmt.Sprintf("Remote MCP %q added (no auth or discovery failed). Its tools will be available on the next message.", a.Name),
			}
			return json.Marshal(result)
		}

		// No auth needed — just add it and update the config.
		if authMeta == nil {
			if updateErr := deps.ConfigUpdater(ctx); updateErr != nil {
				slog.Error("failed to update mcp config", "err", updateErr)
			}
			result := map[string]any{
				"name":    entry.Name,
				"url":     entry.URL,
				"status":  "ready",
				"message": fmt.Sprintf("Remote MCP %q added (no auth required). Its tools will be available on the next message.", a.Name),
			}
			return json.Marshal(result)
		}

		// OAuth required — start the flow.
		if deps.Callback == nil {
			return nil, fmt.Errorf("OAuth is required by %s but no callback server is configured", a.URL)
		}

		slog.Info("remote MCP requires OAuth", "name", a.Name, "issuer", authMeta.Issuer)

		callbackURL := deps.Callback.CallbackURL()

		// Dynamic client registration if supported.
		var reg *mcp.ClientRegistration
		if authMeta.RegistrationEndpoint != "" {
			reg, err = mcp.RegisterClient(ctx, authMeta, callbackURL)
			if err != nil {
				return nil, fmt.Errorf("dynamic client registration: %w", err)
			}
			slog.Info("registered OAuth client", "name", a.Name, "client_id", reg.ClientID)
		} else {
			return nil, fmt.Errorf("remote MCP %q requires OAuth but does not support dynamic client registration — manual client_id configuration not yet supported", a.Name)
		}

		// Store the auth metadata and registration before starting the flow,
		// so the callback handler can find it.
		authData := &connection.RemoteMCPAuth{
			AuthServerIssuer:      authMeta.Issuer,
			AuthorizationEndpoint: authMeta.AuthorizationEndpoint,
			TokenEndpoint:         authMeta.TokenEndpoint,
			RegistrationEndpoint:  authMeta.RegistrationEndpoint,
			ClientID:              reg.ClientID,
			ClientSecret:          reg.ClientSecret,
		}
		if err := deps.Manager.SetRemoteMCPAuth(ctx, a.Name, authData); err != nil {
			return nil, fmt.Errorf("store auth metadata: %w", err)
		}

		// Build a PendingFlow for the OAuth callback. We create a temporary
		// provider.Provider with the discovered OAuth config so the existing
		// callback infrastructure can handle the exchange.
		flow := &oauth.PendingRemoteMCPFlow{
			Name:          a.Name,
			MCPURL:        a.URL,
			AuthMeta:      authMeta,
			ClientReg:     reg,
			Manager:       deps.Manager,
			ConfigUpdater: deps.ConfigUpdater,
		}

		state, codeVerifier, authURL, err := deps.Callback.StartRemoteMCPFlow(flow, callbackURL, a.URL)
		if err != nil {
			return nil, fmt.Errorf("start oauth flow: %w", err)
		}

		// Store the PKCE verifier — it's needed for the token exchange.
		_ = state
		flow.CodeVerifier = codeVerifier

		result := map[string]any{
			"name":    entry.Name,
			"url":     entry.URL,
			"status":  "pending_auth",
			"auth_url": authURL,
			"message": fmt.Sprintf("Send this authorization URL to the user. After they authorize, use connection_auth_wait with connection_id=%q to confirm completion. Once authorized, the remote MCP's tools will be available on the next message.", a.Name),
		}
		return json.Marshal(result)
	}
}

// --- remote_mcp_remove ---

func remoteMCPRemoveDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "remote_mcp_remove",
		Description: "Disconnect a remote MCP server and delete its stored credentials.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "The name of the remote MCP to remove. Use remote_mcp_list to see names."
				}
			},
			"required": ["name"]
		}`),
	}
}

type remoteMCPRemoveArgs struct {
	Name string `json:"name"`
}

func remoteMCPRemoveHandler(deps RemoteMCPToolsDeps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a remoteMCPRemoveArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if err := deps.Manager.RemoveRemoteMCP(ctx, a.Name); err != nil {
			return nil, fmt.Errorf("remove remote mcp: %w", err)
		}

		// Regenerate config to remove the entry.
		if updateErr := deps.ConfigUpdater(ctx); updateErr != nil {
			slog.Error("failed to update mcp config after remove", "err", updateErr)
		}

		return json.Marshal(fmt.Sprintf("Remote MCP %q removed. Its tools will no longer be available on the next message.", a.Name))
	}
}

// --- auth wait tool for remote MCPs ---

// RegisterRemoteMCPAuthWaitTool adds a wait tool that works for remote MCP
// OAuth flows. It reuses connection_auth_wait's pattern but checks remote MCP auth.
// Actually, connection_auth_wait already polls for credentials, but remote MCPs
// store auth differently. We extend it here.
func RegisterRemoteMCPAuthWaitTool(h *mcp.Handler, deps RemoteMCPToolsDeps) {
	h.Register(remoteMCPAuthWaitDef(), remoteMCPAuthWaitHandler(deps))
}

func remoteMCPAuthWaitDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "remote_mcp_auth_wait",
		Description: "Wait for a pending remote MCP OAuth authorization to complete. Call this after sending the auth URL to the user. Blocks until the user finishes authorizing (up to 5 minutes).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "The remote MCP name to wait for."
				}
			},
			"required": ["name"]
		}`),
	}
}

type remoteMCPAuthWaitArgs struct {
	Name string `json:"name"`
}

func remoteMCPAuthWaitHandler(deps RemoteMCPToolsDeps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a remoteMCPAuthWaitArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		// Check if auth is already complete.
		auth, err := deps.Manager.GetRemoteMCPAuth(ctx, a.Name)
		if err != nil {
			return nil, fmt.Errorf("check auth: %w", err)
		}
		if auth != nil && auth.AccessToken != "" {
			result := map[string]string{
				"name":    a.Name,
				"status":  "authorized",
				"message": fmt.Sprintf("Remote MCP %q is authorized. Its tools will be available on the next message.", a.Name),
			}
			return json.Marshal(result)
		}

		// Poll until timeout or cancellation.
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		deadline := time.After(5 * time.Minute)

		for {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("authorization wait cancelled")
			case <-deadline:
				result := map[string]string{
					"name":    a.Name,
					"status":  "timeout",
					"message": fmt.Sprintf("Authorization timed out. The user may not have completed the OAuth flow. Try remote_mcp_add again."),
				}
				return json.Marshal(result)
			case <-ticker.C:
				auth, err := deps.Manager.GetRemoteMCPAuth(ctx, a.Name)
				if err != nil {
					return nil, fmt.Errorf("check auth: %w", err)
				}
				if auth != nil && auth.AccessToken != "" {
					result := map[string]string{
						"name":    a.Name,
						"status":  "authorized",
						"message": fmt.Sprintf("Remote MCP %q is now authorized! Its tools will be available on the next message.", a.Name),
					}
					return json.Marshal(result)
				}
			}
		}
	}
}
