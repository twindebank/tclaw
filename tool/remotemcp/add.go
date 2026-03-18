package remotemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"

	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/mcp/discovery"
)

const (
	maxMCPNameLength = 64
	maxMCPURLLength  = 2048
)

var mcpNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

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
				},
				"channel": {
					"type": "string",
					"description": "Channel name to scope this remote MCP to. Its tools will only be available on this channel."
				}
			},
			"required": ["url", "name", "channel"]
		}`),
	}
}

type remoteMCPAddArgs struct {
	URL     string `json:"url"`
	Name    string `json:"name"`
	Channel string `json:"channel"`
}

func remoteMCPAddHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a remoteMCPAddArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Name == "" || len(a.Name) > maxMCPNameLength || !mcpNamePattern.MatchString(a.Name) {
			return nil, fmt.Errorf("name must be 1-%d characters, alphanumeric with hyphens/underscores", maxMCPNameLength)
		}
		if a.URL == "" || len(a.URL) > maxMCPURLLength {
			return nil, fmt.Errorf("url is required and must be under %d characters", maxMCPURLLength)
		}
		parsed, err := url.Parse(a.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("url must be a valid absolute URL (e.g. https://mcp.example.com/sse)")
		}
		if parsed.Scheme != "https" {
			return nil, fmt.Errorf("only HTTPS MCP server URLs are allowed")
		}
		if a.Channel == "" {
			return nil, fmt.Errorf("channel is required — specify which channel this remote MCP's tools should be available on")
		}

		// Store the remote MCP entry.
		entry, err := deps.Manager.AddRemoteMCP(ctx, a.Name, a.URL, a.Channel)
		if err != nil {
			return nil, fmt.Errorf("add remote mcp: %w", err)
		}

		slog.Info("discovering auth for remote MCP", "name", a.Name, "url", a.URL)

		// Discover whether OAuth is required.
		authMeta, err := discovery.DiscoverAuth(ctx, a.URL)
		if err != nil {
			slog.Warn("auth discovery failed, adding without auth", "name", a.Name, "err", err)
			if updateErr := deps.ConfigUpdater(ctx); updateErr != nil {
				return nil, fmt.Errorf("remote MCP %q added but config update failed — tools won't be available until next restart: %w", a.Name, updateErr)
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
				return nil, fmt.Errorf("remote MCP %q added but config update failed — tools won't be available until next restart: %w", a.Name, updateErr)
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
		var reg *discovery.ClientRegistration
		if authMeta.RegistrationEndpoint != "" {
			reg, err = discovery.RegisterClient(ctx, authMeta, callbackURL)
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

		// Build PKCE auth URL and create the pending flow.
		_, codeVerifier := discovery.BuildAuthURLWithPKCE(authMeta, reg, "", callbackURL, a.URL)

		flow := &pendingRemoteMCPFlow{
			name:          a.Name,
			mcpURL:        a.URL,
			authMeta:      authMeta,
			clientReg:     reg,
			manager:       deps.Manager,
			configUpdater: deps.ConfigUpdater,
			codeVerifier:  codeVerifier,
			done:          make(chan struct{}),
		}

		// Register with the callback server — it generates the state param
		// and will call flow.Complete/Fail when the callback arrives.
		state, err := deps.Callback.RegisterFlow(flow)
		if err != nil {
			return nil, fmt.Errorf("register oauth flow: %w", err)
		}

		// Rebuild the auth URL with the actual state token.
		authURL, _ := discovery.BuildAuthURLWithPKCE(authMeta, reg, state, callbackURL, a.URL)

		result := map[string]any{
			"name":     entry.Name,
			"url":      entry.URL,
			"status":   "pending_auth",
			"auth_url": authURL,
			"message":  fmt.Sprintf("Send this authorization URL to the user. After they authorize, use remote_mcp_auth_wait with name=%q to confirm completion. Once authorized, the remote MCP's tools will be available on the next message.", a.Name),
		}
		return json.Marshal(result)
	}
}
