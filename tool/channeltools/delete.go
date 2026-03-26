package channeltools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

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

		// Look up the dynamic channel first — even if a static channel with the
		// same name exists, we can still delete the dynamic one.
		cfg, err := deps.Registry.DynamicStore().Get(ctx, a.Name)
		if err != nil {
			return nil, fmt.Errorf("look up channel: %w", err)
		}
		if cfg == nil {
			if deps.Registry.IsStatic(a.Name) {
				return nil, fmt.Errorf("channel %q is a static channel (from config file) and cannot be deleted", a.Name)
			}
			return nil, fmt.Errorf("channel %q not found", a.Name)
		}

		if err := deps.Registry.DynamicStore().Remove(ctx, a.Name); err != nil {
			return nil, fmt.Errorf("delete channel: %w", err)
		}

		// Clean up any associated secrets (e.g. bot token). Non-fatal since the
		// channel config is already removed — an orphaned secret is less harmful
		// than telling the agent the delete failed.
		if err := deps.SecretStore.Delete(ctx, channel.ChannelSecretKey(a.Name)); err != nil {
			slog.Warn("failed to clean up channel secret after delete", "channel", a.Name, "err", err)
		}

		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		result := map[string]any{
			"name":    a.Name,
			"message": fmt.Sprintf("Channel %q deleted. The agent will restart automatically to apply the change.", a.Name),
		}
		return json.Marshal(result)
	}
}
