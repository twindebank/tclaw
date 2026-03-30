package notificationtools

import (
	"context"
	"encoding/json"

	"tclaw/mcp"
)

func typesDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolTypes,
		Description: "List all available notification types across tool packages. " +
			"Shows what you can subscribe to, including required parameters and supported scopes. " +
			"Types marked auto_subscribe are managed by the tool package automatically — " +
			"you don't need to subscribe manually, but they appear in notification_list for visibility.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func typesHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		types := deps.Manager.AvailableTypes()

		if len(types) == 0 {
			return json.Marshal("No notification types available. Tool packages with notification support will appear here.")
		}

		return json.Marshal(types)
	}
}
