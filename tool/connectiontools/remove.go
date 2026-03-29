package connectiontools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/connection"
	"tclaw/mcp"
)

const ToolConnectionRemove = "connection_remove"

func connectionRemoveDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolConnectionRemove,
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

func connectionRemoveHandler(mgr *connection.Manager, onDisconnect func(connection.ConnectionID)) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a connectionRemoveArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		id := connection.ConnectionID(a.ConnectionID)
		if err := mgr.Remove(ctx, id); err != nil {
			return nil, fmt.Errorf("remove connection: %w", err)
		}

		if onDisconnect != nil {
			onDisconnect(id)
		}

		return json.Marshal(fmt.Sprintf("Connection %s removed.", id))
	}
}
