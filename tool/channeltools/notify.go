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
		Description: "Send a notification message directly to a channel's users via the platform. " +
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
					"description": "Message to send to channel users."
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

		cfg, err := deps.Registry.DynamicStore().Get(ctx, a.ChannelName)
		if err != nil {
			return nil, fmt.Errorf("look up channel: %w", err)
		}
		if cfg == nil {
			return nil, fmt.Errorf("channel %q not found", a.ChannelName)
		}

		provisioner, ok := deps.Provisioners[cfg.Type]
		if !ok {
			return nil, fmt.Errorf("channel %q (type %s) does not support direct notifications", a.ChannelName, cfg.Type)
		}

		if len(cfg.AllowedUsers) == 0 {
			return nil, fmt.Errorf("channel %q has no allowed_users to send to", a.ChannelName)
		}

		token, err := deps.SecretStore.Get(ctx, channel.ChannelSecretKey(a.ChannelName))
		if err != nil {
			return nil, fmt.Errorf("read channel token: %w", err)
		}
		if token == "" {
			return nil, fmt.Errorf("no token found for channel %q", a.ChannelName)
		}

		sent, err := provisioner.Notify(ctx, token, cfg.AllowedUsers, a.Message)
		if err != nil {
			return nil, fmt.Errorf("notify channel users: %w", err)
		}

		return json.Marshal(map[string]any{
			"status":  "sent",
			"channel": a.ChannelName,
			"sent_to": sent,
		})
	}
}
