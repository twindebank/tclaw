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
		Description: "Update a dynamic channel's description, tool permissions, or rotate its bot token. Cannot modify static channels (from config file). The agent restarts automatically to apply changes.",
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
						},
						"allowed_users": {
							"type": "array",
							"items": {"type": "integer"},
							"description": "Telegram user IDs allowed to interact with this bot. Replaces existing list. At least one user ID is required."
						}
					}
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
				},
				"links": {
					"type": "array",
					"description": "Cross-channel messaging links. Replaces all existing links. Pass empty array to remove all links.",
					"items": {
						"type": "object",
						"properties": {
							"target": {"type": "string", "description": "Name of the target channel."},
							"description": {"type": "string", "description": "When this link should be used."}
						},
						"required": ["target", "description"]
					}
				}
			},
			"required": ["name"]
		}`),
	}
}

type telegramEditConfig struct {
	Token        string   `json:"token"`
	AllowedUsers *[]int64 `json:"allowed_users"`
}

type channelEditArgs struct {
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	TelegramConfig  *telegramEditConfig `json:"telegram_config"`
	AllowedTools    []string            `json:"allowed_tools"`
	DisallowedTools []string            `json:"disallowed_tools"`
	Links           *[]channel.Link     `json:"links"`
}

func channelEditHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelEditArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Description == "" && a.TelegramConfig == nil && a.AllowedTools == nil && a.DisallowedTools == nil && a.Links == nil {
			return nil, fmt.Errorf("at least one of 'description', 'telegram_config', 'allowed_tools', 'disallowed_tools', or 'links' must be provided")
		}

		// telegram_config with only allowed_users (no token) is valid for updating the allowlist.
		if a.TelegramConfig != nil && a.TelegramConfig.Token == "" && a.TelegramConfig.AllowedUsers == nil {
			return nil, fmt.Errorf("telegram_config must include 'token' and/or 'allowed_users'")
		}

		// Reject editing static channels — they are defined in the live config
		// file (gitignored, not in source control). Return a helpful error with
		// the exact file path and YAML snippet so the agent can edit it directly
		// using Read/Edit tools, then deploy to apply the change.
		if deps.Registry.IsStatic(a.Name) {
			return nil, fmt.Errorf(
				"channel %q is a static channel defined in %s.\n\n"+
					"tclaw.yaml is not in git — edit it directly using the Read/Edit tools at that path, "+
					"then run deploy to apply the change.\n\n"+
					"Add a links section under the channel's entry:\n\n"+
					"    channels:\n"+
					"      - name: %s\n"+
					"        links:\n"+
					"          - target: <target-channel>\n"+
					"            description: \"When to use this link\"",
				a.Name, deps.ConfigPath, a.Name,
			)
		}

		// Look up the channel to validate telegram_config is only used on telegram channels.
		cfg, err := deps.Registry.DynamicStore().Get(ctx, a.Name)
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
			if err := deps.Registry.DynamicStore().Update(ctx, a.Name, func(c *channel.DynamicChannelConfig) {
				c.Description = a.Description
			}); err != nil {
				return nil, fmt.Errorf("edit channel: %w", err)
			}
		}

		// Update tool permissions if provided.
		if a.AllowedTools != nil || a.DisallowedTools != nil {
			if err := deps.Registry.DynamicStore().Update(ctx, a.Name, func(c *channel.DynamicChannelConfig) {
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

		// Update Telegram-specific config if provided.
		if a.TelegramConfig != nil {
			// Rotate the bot token if provided.
			if a.TelegramConfig.Token != "" {
				if err := deps.SecretStore.Set(ctx, channel.ChannelSecretKey(a.Name), a.TelegramConfig.Token); err != nil {
					return nil, fmt.Errorf("update channel secret: %w", err)
				}
			}

			// Update allowed users if provided.
			if a.TelegramConfig.AllowedUsers != nil {
				if len(*a.TelegramConfig.AllowedUsers) == 0 {
					return nil, fmt.Errorf("allowed_users cannot be empty — at least one Telegram user ID is required")
				}
				if err := deps.Registry.DynamicStore().Update(ctx, a.Name, func(c *channel.DynamicChannelConfig) {
					c.AllowedUsers = *a.TelegramConfig.AllowedUsers
				}); err != nil {
					return nil, fmt.Errorf("edit channel allowed_users: %w", err)
				}
			}
		}

		// Update links if provided.
		if a.Links != nil {
			// Validate links.
			linkTargets := make(map[string]bool, len(*a.Links))
			for i, link := range *a.Links {
				if link.Target == "" || link.Description == "" {
					return nil, fmt.Errorf("links[%d]: target and description are required", i)
				}
				if link.Target == a.Name {
					return nil, fmt.Errorf("links[%d]: self-links are not allowed", i)
				}
				if linkTargets[link.Target] {
					return nil, fmt.Errorf("links[%d]: duplicate target %q", i, link.Target)
				}
				linkTargets[link.Target] = true
			}
			if err := deps.Registry.DynamicStore().Update(ctx, a.Name, func(c *channel.DynamicChannelConfig) {
				c.Links = *a.Links
			}); err != nil {
				return nil, fmt.Errorf("edit channel links: %w", err)
			}
		}

		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		result := map[string]any{
			"name":    a.Name,
			"message": fmt.Sprintf("Channel %q updated. The agent will restart automatically to apply changes.", a.Name),
		}
		if a.Description != "" {
			result["description"] = a.Description
		}
		if a.TelegramConfig != nil && a.TelegramConfig.Token != "" {
			result["token_rotated"] = true
		}
		return json.Marshal(result)
	}
}
