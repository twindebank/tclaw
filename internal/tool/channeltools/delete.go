package channeltools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"tclaw/internal/channel"
	"tclaw/internal/mcp"
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

		// Tear down platform resources synchronously before removing config.
		// This mirrors channel_create's synchronous provisioning so the agent
		// gets immediate feedback on success/failure and no half-states.
		runtimeState, err := deps.RuntimeState.Get(ctx, a.Name)
		if err != nil {
			return nil, fmt.Errorf("read runtime state: %w", err)
		}
		if runtimeState.TeardownState.HasTeardownState() {
			entry := deps.Registry.ByName(a.Name)
			if entry != nil {
				provisioner := deps.Provisioners.Get(entry.Type)
				if provisioner != nil {
					if teardownErr := provisioner.Teardown(ctx, runtimeState.TeardownState); teardownErr != nil {
						return nil, fmt.Errorf("platform teardown failed for channel %q (channel NOT deleted — retry or clean up manually): %w", a.Name, teardownErr)
					}
				} else {
					slog.Error("no provisioner for channel type, skipping platform teardown",
						"channel", a.Name, "type", entry.Type)
				}
			}
		}

		if err := deps.ConfigWriter.RemoveChannel(deps.UserID, a.Name); err != nil {
			return nil, fmt.Errorf("delete channel from config: %w", err)
		}

		// Clean up runtime state and secrets. Best-effort since the config
		// entry is already removed — an orphaned secret is less harmful than
		// telling the agent the delete failed.
		if err := deps.RuntimeState.Delete(ctx, a.Name); err != nil {
			slog.Error("failed to clean up runtime state after delete", "channel", a.Name, "err", err)
		}
		if err := deps.SecretStore.Delete(ctx, channel.ChannelSecretKey(a.Name)); err != nil {
			slog.Error("failed to clean up channel secret after delete", "channel", a.Name, "err", err)
		}
		if deps.MemoryDir != "" {
			knowledgeDir := filepath.Join(deps.MemoryDir, "channels", a.Name)
			if err := os.RemoveAll(knowledgeDir); err != nil {
				slog.Warn("failed to clean up channel knowledge dir", "channel", a.Name, "dir", knowledgeDir, "err", err)
			}
		}

		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		return json.Marshal(map[string]string{
			"name":    a.Name,
			"message": fmt.Sprintf("Channel %q deleted. The agent will restart automatically.", a.Name),
		})
	}
}
