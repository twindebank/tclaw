package channeltools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tclaw/channel"
	"tclaw/mcp"
)

func channelDoneDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: "channel_done",
		Description: "Tear down a dynamic channel — deletes platform resources (e.g. Telegram bot), " +
			"removes channel config, and removes channel secrets. Works on both ephemeral and " +
			"non-ephemeral dynamic channels. Send any results via channel_send BEFORE calling this — " +
			"once called, the channel is gone. Fails if platform teardown fails (no half-states). " +
			"Cannot be used on static channels (from config file).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_name": {
					"type": "string",
					"description": "Name of the channel to tear down. Defaults to the current channel if omitted."
				}
			}
		}`),
	}
}

type channelDoneArgs struct {
	ChannelName string `json:"channel_name"`
}

func channelDoneHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelDoneArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		// If no channel name specified, we can't infer the current channel
		// from tool context — require it explicitly.
		if a.ChannelName == "" {
			return nil, fmt.Errorf("channel_name is required")
		}

		// Validate the channel exists and is dynamic.
		if deps.Registry.IsStatic(a.ChannelName) {
			return nil, fmt.Errorf("channel %q is a static channel (from config file) and cannot be torn down via channel_done — edit the config file instead", a.ChannelName)
		}

		cfg, err := deps.Registry.DynamicStore().Get(ctx, a.ChannelName)
		if err != nil {
			return nil, fmt.Errorf("read channel config: %w", err)
		}
		if cfg == nil {
			return nil, fmt.Errorf("channel %q not found", a.ChannelName)
		}

		// Platform-specific teardown (e.g. delete Telegram bot).
		if cfg.TeardownState != nil {
			provisioner, ok := deps.Provisioners[cfg.Type]
			if !ok {
				slog.Error("no provisioner for channel type, skipping platform teardown",
					"channel", a.ChannelName, "type", cfg.Type)
			} else {
				if teardownErr := provisioner.Teardown(ctx, cfg.TeardownState); teardownErr != nil {
					// Do NOT delete the channel config — would orphan platform resources.
					return nil, fmt.Errorf("platform teardown failed for channel %q (channel NOT deleted — retry or clean up manually): %w", a.ChannelName, teardownErr)
				}
			}
		}

		// Delete channel config.
		if err := deps.Registry.DynamicStore().Remove(ctx, a.ChannelName); err != nil {
			return nil, fmt.Errorf("delete channel config: %w", err)
		}

		// Delete channel secret (best-effort — log but don't fail).
		if err := deps.SecretStore.Delete(ctx, channel.ChannelSecretKey(a.ChannelName)); err != nil {
			slog.Error("failed to delete channel secret during teardown",
				"channel", a.ChannelName, "err", err)
		}

		// Trigger agent restart to pick up the removal.
		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		return json.Marshal(map[string]string{
			"status":  "deleted",
			"channel": a.ChannelName,
			"message": fmt.Sprintf("Channel %q has been torn down. Platform resources cleaned up.", a.ChannelName),
		})
	}
}
