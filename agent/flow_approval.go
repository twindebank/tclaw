package agent

import (
	"context"
	"log/slog"
	"strings"

	"tclaw/channel"
)

// handleToolApprovalFlow processes a message within a tool approval flow.
// Returns a FlowResult — if the user approves, FallThroughMsg is set so
// the caller dispatches the original message to handle() with expanded tools.
func handleToolApprovalFlow(
	ctx context.Context,
	opts Options,
	fm *FlowManager,
	approval *pendingToolApproval,
	ch channel.Channel,
	msg channel.TaggedMessage,
	sessions map[channel.ChannelID]string,
) FlowResult {
	answer := strings.TrimSpace(strings.ToLower(msg.Text))

	switch answer {
	case "approve", "yes", "y":
		slog.Info("tool approval granted, retrying",
			"channel", msg.ChannelID, "tools", approval.deniedTools)

		sessions[msg.ChannelID] = approval.sessionID
		retryMsg, override, restore := buildApprovalOverride(opts, approval, msg.ChannelID)
		fm.Complete(msg.ChannelID)

		// Apply the temporary override.
		if opts.ChannelToolOverrides == nil {
			opts.ChannelToolOverrides = make(map[channel.ChannelID]ChannelToolPermissions)
		}
		opts.ChannelToolOverrides[msg.ChannelID] = override

		return FlowResult{
			Handled:        false, // caller should dispatch retryMsg to handle()
			FallThroughMsg: &retryMsg,
			RestoreFunc:    restore,
		}

	case "no", "n", "cancel":
		fm.Complete(msg.ChannelID)
		if _, err := ch.Send(ctx, "↩️ Tool approval cancelled."); err != nil {
			slog.Error("failed to send approval cancel", "err", err)
		}
		if err := ch.Done(ctx); err != nil {
			slog.Error("failed to close turn after approval cancel", "err", err)
		}
		return FlowResult{Handled: true}

	default:
		// Any other message clears the approval and is processed normally.
		fm.Complete(msg.ChannelID)
		return FlowResult{
			Handled:        false,
			FallThroughMsg: &msg,
		}
	}
}
