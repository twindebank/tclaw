package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/channel"
	"tclaw/mcp"
)

func channelEditDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "channel_edit",
		Description: "Update a dynamic channel's description. Cannot modify static channels (from config file). Changes take effect after agent restart.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "The name of the channel to edit. Use channel_list to see available channels."
				},
				"description": {
					"type": "string",
					"description": "New description for the channel."
				}
			},
			"required": ["name", "description"]
		}`),
	}
}

type channelEditArgs struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func channelEditHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelEditArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		// Reject editing static channels.
		for _, info := range deps.StaticChannels {
			if info.Name == a.Name {
				return nil, fmt.Errorf("channel %q is a static channel (from config file) and cannot be edited. Only dynamic channels can be modified.", a.Name)
			}
		}

		if err := deps.DynamicStore.Update(ctx, a.Name, func(cfg *channel.DynamicChannelConfig) {
			cfg.Description = a.Description
		}); err != nil {
			return nil, fmt.Errorf("edit channel: %w", err)
		}

		result := map[string]any{
			"name":        a.Name,
			"description": a.Description,
			"message":     fmt.Sprintf("Channel %q updated. Changes take effect after agent restart — send 'stop' or wait for idle timeout.", a.Name),
		}
		return json.Marshal(result)
	}
}
