package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/channel"
	"tclaw/mcp"
)

func channelPingDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: "channel_ping",
		Description: "Send a message from a Telegram bot to its allowed users. " +
			"Useful for making a newly created bot appear in users' Telegram sidebars " +
			"without them having to find and /start it manually. " +
			"The message is sent as the bot, not as the user.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_name": {
					"type": "string",
					"description": "Name of the Telegram channel to ping."
				},
				"message": {
					"type": "string",
					"description": "Message to send. Supports Telegram HTML formatting."
				}
			},
			"required": ["channel_name", "message"]
		}`),
	}
}

type channelPingArgs struct {
	ChannelName string `json:"channel_name"`
	Message     string `json:"message"`
}

func channelPingHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelPingArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.ChannelName == "" {
			return nil, fmt.Errorf("channel_name is required")
		}
		if a.Message == "" {
			return nil, fmt.Errorf("message is required")
		}

		// Look up the channel config to get allowed_users.
		cfg, err := deps.Registry.DynamicStore().Get(ctx, a.ChannelName)
		if err != nil {
			return nil, fmt.Errorf("look up channel: %w", err)
		}
		if cfg == nil {
			return nil, fmt.Errorf("channel %q not found", a.ChannelName)
		}
		if cfg.Type != channel.TypeTelegram {
			return nil, fmt.Errorf("channel %q is not a Telegram channel", a.ChannelName)
		}
		if len(cfg.AllowedUsers) == 0 {
			return nil, fmt.Errorf("channel %q has no allowed_users to send to", a.ChannelName)
		}

		// Read the bot token from the secret store.
		token, err := deps.SecretStore.Get(ctx, channel.ChannelSecretKey(a.ChannelName))
		if err != nil {
			return nil, fmt.Errorf("read bot token: %w", err)
		}
		if token == "" {
			return nil, fmt.Errorf("no bot token found for channel %q", a.ChannelName)
		}

		// Send to all allowed users.
		var sent int
		for _, userID := range cfg.AllowedUsers {
			sendBotGreeting(token, userID, a.Message)
			sent++
		}

		return json.Marshal(map[string]any{
			"status":  "sent",
			"channel": a.ChannelName,
			"sent_to": sent,
		})
	}
}
