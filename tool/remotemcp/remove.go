package remotemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tclaw/mcp"
)

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

func remoteMCPRemoveHandler(deps Deps) mcp.ToolHandler {
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
