package remotemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/mcp"
)

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

func remoteMCPAuthWaitHandler(deps Deps) mcp.ToolHandler {
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
					"message": "Authorization timed out. The user may not have completed the OAuth flow. Try remote_mcp_add again.",
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
