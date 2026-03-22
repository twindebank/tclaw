package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/mcp"
)

func channelIsBusyDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "channel_is_busy",
		Description: "Check whether a channel is currently busy — either actively processing a turn or in an ongoing conversation (within the idle window). Use this before sending a cross-channel message to decide whether to deliver immediately or defer.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_name": {
					"type": "string",
					"description": "Name of the channel to check (e.g. \"assistant\", \"admin\")."
				}
			},
			"required": ["channel_name"]
		}`),
	}
}

type channelIsBusyArgs struct {
	ChannelName string `json:"channel_name"`
}

func channelIsBusyHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelIsBusyArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.ChannelName == "" {
			return nil, fmt.Errorf("channel_name is required")
		}
		if deps.ActivityTracker == nil {
			return nil, fmt.Errorf("activity tracking is not available")
		}

		busy := deps.ActivityTracker.IsBusy(a.ChannelName)
		return json.Marshal(map[string]any{"busy": busy})
	}
}
