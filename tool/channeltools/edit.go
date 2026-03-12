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
		Description: "Update a dynamic channel's description or rotate its bot token. Cannot modify static channels (from config file). Changes take effect after agent restart.",
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
				},
				"telegram_config": {
					"type": "object",
					"description": "Updated Telegram configuration. Only applicable to telegram channels.",
					"properties": {
						"token": {
							"type": "string",
							"description": "New bot token to replace the existing one. Stored encrypted."
						}
					},
					"required": ["token"]
				},
				"allowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tools this channel is allowed to use. Replaces any existing allowed_tools for this channel."
				},
				"disallowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tools explicitly denied on this channel. Replaces any existing disallowed_tools."
				}
			},
			"required": ["name"]
		}`),
	}
}

type telegramEditConfig struct {
	Token string `json:"token"`
}

type channelEditArgs struct {
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	TelegramConfig  *telegramEditConfig `json:"telegram_config"`
	AllowedTools    []string            `json:"allowed_tools"`
	DisallowedTools []string            `json:"disallowed_tools"`
}

func channelEditHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelEditArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Description == "" && a.TelegramConfig == nil && a.AllowedTools == nil && a.DisallowedTools == nil {
			return nil, fmt.Errorf("at least one of 'description', 'telegram_config', 'allowed_tools', or 'disallowed_tools' must be provided")
		}

		// Reject editing static channels.
		for _, info := range deps.StaticChannels {
			if info.Name == a.Name {
				return nil, fmt.Errorf("channel %q is a static channel (from config file) and cannot be edited. Only dynamic channels can be modified.", a.Name)
			}
		}

		// Look up the channel to validate telegram_config is only used on telegram channels.
		cfg, err := deps.DynamicStore.Get(ctx, a.Name)
		if err != nil {
			return nil, fmt.Errorf("look up channel: %w", err)
		}
		if cfg == nil {
			return nil, fmt.Errorf("dynamic channel %q not found", a.Name)
		}

		if a.TelegramConfig != nil && cfg.Type != channel.TypeTelegram {
			return nil, fmt.Errorf("telegram_config can only be used with telegram channels, but %q is a %s channel", a.Name, cfg.Type)
		}

		// Update the description if provided.
		if a.Description != "" {
			if err := deps.DynamicStore.Update(ctx, a.Name, func(c *channel.DynamicChannelConfig) {
				c.Description = a.Description
			}); err != nil {
				return nil, fmt.Errorf("edit channel: %w", err)
			}
		}

		// Update tool permissions if provided.
		if a.AllowedTools != nil || a.DisallowedTools != nil {
			if err := deps.DynamicStore.Update(ctx, a.Name, func(c *channel.DynamicChannelConfig) {
				if a.AllowedTools != nil {
					c.AllowedTools = a.AllowedTools
				}
				if a.DisallowedTools != nil {
					c.DisallowedTools = a.DisallowedTools
				}
			}); err != nil {
				return nil, fmt.Errorf("edit channel tools: %w", err)
			}
		}

		// Rotate the bot token if provided.
		if a.TelegramConfig != nil {
			if a.TelegramConfig.Token == "" {
				return nil, fmt.Errorf("telegram_config.token must not be empty")
			}
			if err := deps.SecretStore.Set(ctx, channel.ChannelSecretKey(a.Name), a.TelegramConfig.Token); err != nil {
				return nil, fmt.Errorf("update channel secret: %w", err)
			}
		}

		result := map[string]any{
			"name":    a.Name,
			"message": fmt.Sprintf("Channel %q updated. Changes take effect after agent restart — send 'stop' or wait for idle timeout.", a.Name),
		}
		if a.Description != "" {
			result["description"] = a.Description
		}
		if a.TelegramConfig != nil {
			result["token_rotated"] = true
		}
		return json.Marshal(result)
	}
}
