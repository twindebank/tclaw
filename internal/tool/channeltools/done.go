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

const ToolChannelDone = "channel_done"

func channelDoneDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolChannelDone,
		Description: "Tear down a channel — deletes platform resources (e.g. Telegram bot), " +
			"removes channel from config, and cleans up secrets. " +
			"Fails if platform teardown fails (no half-states).\n\n" +
			"IMPORTANT: This tool manages the entire confirmation flow. For channels with a user chat " +
			"(e.g. Telegram), it sends a confirmation prompt to the user and returns immediately with " +
			"status \"awaiting_confirmation\". The router then waits for the user's \"yes\" reply and " +
			"completes teardown — no further agent turn is needed or expected.\n\n" +
			"CRITICAL: After calling this tool, you MUST go completely silent. Do NOT send any messages " +
			"(no \"awaiting your confirmation\", no \"channel closed\", nothing). Any message sent after " +
			"this call may cause a double-message and interfere with teardown. The tool handles " +
			"everything — your job is done the moment this call returns.\n\n" +
			"REQUIRED: Before calling this, you MUST send all results to other channels via channel_send. " +
			"The results_sent field is mandatory.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_name": {
					"type": "string",
					"description": "Name of the channel to tear down. Defaults to the current channel if omitted."
				},
				"results_sent": {
					"type": "string",
					"description": "Required. Describe what results were sent via channel_send before teardown."
				}
			},
			"required": ["results_sent"]
		}`),
	}
}

type channelDoneArgs struct {
	ChannelName string `json:"channel_name"`
	ResultsSent string `json:"results_sent"`
}

func channelDoneHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelDoneArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.ResultsSent == "" {
			return nil, fmt.Errorf("results_sent is required — describe what was sent via channel_send before teardown")
		}

		if a.ChannelName == "" {
			if deps.ActiveChannel != nil {
				a.ChannelName = deps.ActiveChannel()
			}
			if a.ChannelName == "" {
				return nil, fmt.Errorf("channel_name is required")
			}
		}

		if !deps.Registry.NameExists(a.ChannelName) {
			return nil, fmt.Errorf("channel %q not found", a.ChannelName)
		}

		entry := deps.Registry.ByName(a.ChannelName)
		if entry == nil {
			return nil, fmt.Errorf("channel %q not found", a.ChannelName)
		}

		runtimeState, err := deps.RuntimeState.Get(ctx, a.ChannelName)
		if err != nil {
			return nil, fmt.Errorf("read runtime state: %w", err)
		}

		// For channels with platform state (e.g. Telegram chat ID), send a
		// confirmation prompt and return immediately. The router intercepts the
		// user's reply asynchronously via PendingDone.
		if runtimeState.PlatformState.HasPlatformState() {
			provisioner := deps.Provisioners.Get(entry.Type)
			if provisioner == nil {
				return nil, fmt.Errorf("no provisioner for channel type %q — cannot send teardown confirmation", entry.Type)
			}

			token, tokenErr := deps.SecretStore.Get(ctx, channel.ChannelSecretKey(a.ChannelName))
			if tokenErr != nil {
				return nil, fmt.Errorf("read channel token for confirmation: %w", tokenErr)
			}

			if promptErr := provisioner.SendTeardownPrompt(ctx, token, runtimeState.PlatformState); promptErr != nil {
				return nil, fmt.Errorf("send teardown prompt for channel %q: %w", a.ChannelName, promptErr)
			}

			if updateErr := deps.RuntimeState.Update(ctx, a.ChannelName, func(rs *channel.RuntimeState) {
				rs.PendingDone = true
			}); updateErr != nil {
				return nil, fmt.Errorf("set pending_done for channel %q: %w", a.ChannelName, updateErr)
			}

			slog.Info("channel_done: confirmation prompt sent, awaiting user reply", "channel", a.ChannelName)
			return json.Marshal(map[string]string{
				"status":  "awaiting_confirmation",
				"channel": a.ChannelName,
				"message": fmt.Sprintf("Confirmation prompt sent to channel %q. Teardown will complete when the user replies \"yes\".", a.ChannelName),
			})
		}

		// No platform chat — tear down immediately.
		if runtimeState.TeardownState.HasTeardownState() {
			provisioner := deps.Provisioners.Get(entry.Type)
			if provisioner == nil {
				slog.Error("no provisioner for channel type, skipping platform teardown",
					"channel", a.ChannelName, "type", entry.Type)
			} else {
				if teardownErr := provisioner.Teardown(ctx, runtimeState.TeardownState); teardownErr != nil {
					return nil, fmt.Errorf("platform teardown failed for channel %q (channel NOT deleted — retry or clean up manually): %w", a.ChannelName, teardownErr)
				}
			}
		}

		// Remove from config.
		if err := deps.ConfigWriter.RemoveChannel(deps.UserID, a.ChannelName); err != nil {
			return nil, fmt.Errorf("delete channel from config: %w", err)
		}

		// Clean up runtime state, secret, and knowledge dir (best-effort).
		if err := deps.RuntimeState.Delete(ctx, a.ChannelName); err != nil {
			slog.Error("failed to delete runtime state during teardown", "channel", a.ChannelName, "err", err)
		}
		if err := deps.SecretStore.Delete(ctx, channel.ChannelSecretKey(a.ChannelName)); err != nil {
			slog.Error("failed to delete channel secret during teardown", "channel", a.ChannelName, "err", err)
		}
		if deps.MemoryDir != "" {
			knowledgeDir := filepath.Join(deps.MemoryDir, "channels", a.ChannelName)
			if err := os.RemoveAll(knowledgeDir); err != nil {
				slog.Warn("failed to clean up channel knowledge dir", "channel", a.ChannelName, "err", err)
			}
		}

		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		return json.Marshal(map[string]string{
			"status":       "deleted",
			"channel":      a.ChannelName,
			"results_sent": a.ResultsSent,
			"message":      fmt.Sprintf("Channel %q has been torn down.", a.ChannelName),
		})
	}
}
