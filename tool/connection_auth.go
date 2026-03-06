package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/connection"
	"tclaw/mcp"
)

const authWaitTimeout = 5 * time.Minute

// RegisterAuthWaitTool adds the connection_auth_wait tool. Separated from
// RegisterConnectionTools because it's only useful when OAuth is configured.
func RegisterAuthWaitTool(h *mcp.Handler, mgr *connection.Manager) {
	h.Register(connectionAuthWaitDef(), connectionAuthWaitHandler(mgr))
}

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
		// The OAuth callback stores creds asynchronously, so we check periodically.
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
