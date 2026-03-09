package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/mcp"
)

func channelDeleteDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "channel_delete",
		Description: "Delete a dynamic channel. Cannot delete static channels (from config file). The channel stops listening after agent restart.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "The name of the channel to delete. Use channel_list to see available channels."
				}
			},
			"required": ["name"]
		}`),
	}
}

type channelDeleteArgs struct {
	Name string `json:"name"`
}

func channelDeleteHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelDeleteArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		// Reject deleting static channels.
		for _, info := range deps.StaticChannels {
			if info.Name == a.Name {
				return nil, fmt.Errorf("channel %q is a static channel (from config file) and cannot be deleted. Only dynamic channels can be removed.", a.Name)
			}
		}

		if err := deps.DynamicStore.Remove(ctx, a.Name); err != nil {
			return nil, fmt.Errorf("delete channel: %w", err)
		}

		return json.Marshal(fmt.Sprintf("Channel %q deleted. It will stop listening after agent restart — send 'stop' or wait for idle timeout.", a.Name))
	}
}
