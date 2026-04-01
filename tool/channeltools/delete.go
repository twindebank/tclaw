package channeltools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"tclaw/channel"
	"tclaw/mcp"
)

const ToolChannelDelete = "channel_delete"

func channelDeleteDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolChannelDelete,
		Description: "Delete a channel. Removes it from config and cleans up any platform resources. The agent restarts automatically to apply the change.",
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

		if !deps.Registry.NameExists(a.Name) {
			return nil, fmt.Errorf("channel %q not found", a.Name)
		}

		if err := deps.ConfigWriter.RemoveChannel(deps.UserID, a.Name); err != nil {
			return nil, fmt.Errorf("delete channel from config: %w", err)
		}

		// Clean up runtime state and secrets. Best-effort since the config
		// entry is already removed — an orphaned secret is less harmful than
		// telling the agent the delete failed.
		var warnings []string

		if err := deps.RuntimeState.Delete(ctx, a.Name); err != nil {
			slog.Warn("failed to clean up runtime state after delete", "channel", a.Name, "err", err)
			warnings = append(warnings, fmt.Sprintf("runtime state cleanup: %v", err))
		}

		if err := deps.SecretStore.Delete(ctx, channel.ChannelSecretKey(a.Name)); err != nil {
			slog.Warn("failed to clean up channel secret after delete", "channel", a.Name, "err", err)
			warnings = append(warnings, fmt.Sprintf("secret cleanup: %v", err))
		}

		if deps.MemoryDir != "" {
			knowledgeDir := filepath.Join(deps.MemoryDir, "channels", a.Name)
			if err := os.RemoveAll(knowledgeDir); err != nil {
				slog.Warn("failed to clean up channel knowledge dir", "channel", a.Name, "dir", knowledgeDir, "err", err)
				warnings = append(warnings, fmt.Sprintf("knowledge dir cleanup: %v", err))
			}
		}

		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		msg := fmt.Sprintf("Channel %q deleted. The agent will restart automatically.", a.Name)
		if len(warnings) > 0 {
			msg += fmt.Sprintf(" Warnings: %v", warnings)
		}

		return json.Marshal(map[string]string{
			"name":    a.Name,
			"message": msg,
		})
	}
}
