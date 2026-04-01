package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/channel"
	"tclaw/mcp"
)

const ToolChannelNotify = "channel_notify"

func channelNotifyDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolChannelNotify,
		Description: "Send a notification message directly to a channel's user via the platform. " +
			"Useful for making newly created channels visible or sending out-of-band alerts. " +
			"Only works for channel types that support direct notifications.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_name": {
					"type": "string",
					"description": "Name of the channel to notify."
				},
				"message": {
					"type": "string",
					"description": "Message to send to the channel's user."
				}
			},
			"required": ["channel_name", "message"]
		}`),
	}
}

type channelNotifyArgs struct {
	ChannelName string `json:"channel_name"`
	Message     string `json:"message"`
}

func channelNotifyHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelNotifyArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.ChannelName == "" {
			return nil, fmt.Errorf("channel_name is required")
		}
		if a.Message == "" {
			return nil, fmt.Errorf("message is required")
		}

		entry := deps.Registry.ByName(a.ChannelName)
		if entry == nil {
			return nil, fmt.Errorf("channel %q not found", a.ChannelName)
		}

		provisioner := deps.Provisioners.Get(entry.Type)
		if provisioner == nil {
			return nil, fmt.Errorf("channel %q (type %s) does not support direct notifications", a.ChannelName, entry.Type)
		}

		token, err := deps.SecretStore.Get(ctx, channel.ChannelSecretKey(a.ChannelName))
		if err != nil {
			return nil, fmt.Errorf("read channel token: %w", err)
		}
		if token == "" {
			return nil, fmt.Errorf("no token found for channel %q", a.ChannelName)
		}

		if err := provisioner.Notify(ctx, token, a.Message); err != nil {
			return nil, fmt.Errorf("notify channel user: %w", err)
		}

		return json.Marshal(map[string]any{
			"status":  "sent",
			"channel": a.ChannelName,
		})
	}
}
