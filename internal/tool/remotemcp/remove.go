package remotemcp

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/internal/mcp"
)

const ToolRemoteMCPRemove = "remote_mcp_remove"

func remoteMCPRemoveDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolRemoteMCPRemove,
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

func remoteMCPRemoveHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a remoteMCPRemoveArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if err := deps.Manager.RemoveRemoteMCP(ctx, a.Name); err != nil {
			return nil, fmt.Errorf("remove remote mcp: %w", err)
		}

		// Regenerate config to remove the entry. If this fails, the MCP is
		// deleted from storage but may still appear in config until restart.
		if updateErr := deps.ConfigUpdater(ctx); updateErr != nil {
			return nil, fmt.Errorf("remote MCP %q removed from storage but config update failed — tools may persist until restart: %w", a.Name, updateErr)
		}
		// Restart the agent so the CLI drops the now-stale tool allowlist
		// entries for this server.
		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		result := map[string]any{
			"name":    a.Name,
			"message": fmt.Sprintf("Remote MCP %q removed. Its tools will no longer be available on the next message.", a.Name),
		}
		return json.Marshal(result)
	}
}
