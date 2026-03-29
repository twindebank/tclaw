package channeltools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/channel"
	"tclaw/mcp"
)

const ToolChannelIsBusy = "channel_is_busy"

func channelIsBusyDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolChannelIsBusy,
		Description: "Check whether a channel is currently busy — either actively processing a turn " +
			"or in an ongoing conversation (within the idle window). Use this before sending a " +
			"cross-channel message to decide whether to deliver immediately or defer. " +
			"For scheduled jobs that should wait for a user conversation to finish, use a longer " +
			"idle_timeout (e.g. 600 for 10 minutes) — or use channel_send_when_free which handles waiting automatically.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_name": {
					"type": "string",
					"description": "Name of the channel to check (e.g. \"assistant\", \"admin\")."
				},
				"idle_timeout": {
					"type": "integer",
					"description": "How many seconds after the last message to still consider the channel busy. Defaults to 180 (3 minutes). Use longer values for 'wait until conversation is truly done' checks."
				}
			},
			"required": ["channel_name"]
		}`),
	}
}

type channelIsBusyArgs struct {
	ChannelName string `json:"channel_name"`
	IdleTimeout int    `json:"idle_timeout"`
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

		timeout := channel.DefaultIdleTimeout
		if a.IdleTimeout > 0 {
			timeout = time.Duration(a.IdleTimeout) * time.Second
		}

		busy := deps.ActivityTracker.IsBusyWithTimeout(a.ChannelName, timeout)
		return json.Marshal(map[string]any{"busy": busy})
	}
}
