package remotemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"tclaw/internal/mcp"
	"tclaw/internal/mcp/discovery"
	"tclaw/internal/remotemcpstore"
)

const ToolRemoteMCPAuthWait = "remote_mcp_auth_wait"

func remoteMCPAuthWaitDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolRemoteMCPAuthWait,
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
			return finalizeAuthorized(ctx, deps, a.Name, auth)
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
					return finalizeAuthorized(ctx, deps, a.Name, auth)
				}
			}
		}
	}
}

// finalizeAuthorized fetches the tool list from the newly-authorized server
// (using the freshly-issued bearer token) and persists it. The Claude CLI
// can't expand MCP permission globs without this list, so tools/list MUST
// happen post-auth for the OAuth path to produce a working registration.
func finalizeAuthorized(ctx context.Context, deps Deps, name string, auth *remotemcpstore.RemoteMCPAuth) (json.RawMessage, error) {
	entry, err := deps.Manager.GetRemoteMCP(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("look up remote mcp %q: %w", name, err)
	}
	if entry == nil {
		return nil, fmt.Errorf("remote mcp %q disappeared before finalisation", name)
	}

	if len(entry.ToolNames) == 0 {
		headers := map[string]string{"Authorization": "Bearer " + auth.AccessToken}
		toolNames, listErr := discovery.ListTools(ctx, entry.URL, headers, listToolsOpts(deps)...)
		if listErr != nil {
			// Auth worked but tool discovery didn't. Return the error — the
			// registration is unusable without the tool list and the user
			// can see what went wrong rather than silently having a broken
			// MCP registered.
			return nil, fmt.Errorf("authorized but failed to list tools: %w", listErr)
		}
		if len(toolNames) == 0 {
			return nil, fmt.Errorf("remote MCP %q exposed no tools", name)
		}
		if err := deps.Manager.SetToolNames(ctx, name, toolNames); err != nil {
			return nil, fmt.Errorf("persist tool names: %w", err)
		}
		slog.Info("captured tool names for authorised remote mcp", "name", name, "count", len(toolNames))
	}

	if deps.ConfigUpdater != nil {
		if err := deps.ConfigUpdater(ctx); err != nil {
			slog.Warn("config update after auth failed", "name", name, "err", err)
		}
	}
	// Restart the agent so the CLI picks up the expanded tool allowlist.
	if deps.OnChannelChange != nil {
		deps.OnChannelChange()
	}

	result := map[string]string{
		"name":    name,
		"status":  "authorized",
		"message": fmt.Sprintf("Remote MCP %q is authorized. Its tools will be available on the next message.", name),
	}
	return json.Marshal(result)
}
