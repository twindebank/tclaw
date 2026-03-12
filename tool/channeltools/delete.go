package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/channel"
	"tclaw/mcp"
)

func channelDeleteDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "channel_delete",
		Description: "Delete a dynamic channel. Cannot delete static channels (from config file). The agent restarts automatically to apply the change.",
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

		// Look up the channel before deleting so we know its type.
		cfg, err := deps.DynamicStore.Get(ctx, a.Name)
		if err != nil {
			return nil, fmt.Errorf("look up channel: %w", err)
		}
		if cfg == nil {
			return nil, fmt.Errorf("dynamic channel %q not found", a.Name)
		}

		if err := deps.DynamicStore.Remove(ctx, a.Name); err != nil {
			return nil, fmt.Errorf("delete channel: %w", err)
		}

		// Clean up any associated secrets (e.g. Telegram bot token).
		if cfg.Type == channel.TypeTelegram {
			if err := deps.SecretStore.Delete(ctx, channel.ChannelSecretKey(a.Name)); err != nil {
				// Log but don't fail — the channel config is already removed.
				// An orphaned secret is less harmful than a failed delete.
				return nil, fmt.Errorf("channel deleted but failed to remove secret: %w", err)
			}
		}

		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		return json.Marshal(fmt.Sprintf("Channel %q deleted. The agent will restart automatically to apply the change.", a.Name))
	}
}
