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
			"non-ephemeral dynamic channels. Fails if platform teardown fails (no half-states). " +
			"Cannot be used on static channels (from config file).\n\n" +
			"REQUIRED: Before calling this, you MUST send all results to other channels via channel_send " +
			"(PR URLs, summaries, findings, etc.). The results_sent field is mandatory — provide a brief " +
			"summary of what was sent and to which channel(s). If you have outbound links but sent nothing, " +
			"send results first. If there are genuinely no results to report (e.g. no outbound links, or " +
			"task produced nothing noteworthy), explain why in results_sent.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_name": {
					"type": "string",
					"description": "Name of the channel to tear down. Defaults to the current channel if omitted."
				},
				"results_sent": {
					"type": "string",
					"description": "Required. Describe what results were sent via channel_send before teardown (e.g. 'Sent PR #57 URL and summary to admin'). If nothing was sent, explain why (e.g. 'No outbound links configured' or 'Task produced no results')."
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
			return nil, fmt.Errorf("results_sent is required — describe what was sent via channel_send before teardown, or explain why nothing was sent")
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
			"status":       "deleted",
			"channel":      a.ChannelName,
			"results_sent": a.ResultsSent,
			"message":      fmt.Sprintf("Channel %q has been torn down. Platform resources cleaned up.", a.ChannelName),
		})
	}
}
