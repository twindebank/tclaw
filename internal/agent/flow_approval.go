package agent

import (
	"context"
	"log/slog"
	"strings"

	"tclaw/internal/channel"
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
	if press := channel.ParseButtonPress(msg.Text); press != nil {
		if press.PromptID != approval.promptID {
			// An older prompt's button. The approval stays open for this one.
			sendStaleButtonNotice(ctx, opts, msg.ChannelID)
			return FlowResult{Handled: true}
		}
		answer = string(press.Reply)
	}

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
		if _, err := opts.send(ctx, msg.ChannelID, "↩️ Tool approval cancelled."); err != nil {
			slog.Error("failed to send approval cancel", "err", err)
		}
		if err := opts.done(ctx, msg.ChannelID); err != nil {
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

// sendStaleButtonNotice tells the user a button they pressed answers nothing any more.
func sendStaleButtonNotice(ctx context.Context, opts Options, chID channel.ChannelID) {
	if _, err := opts.send(ctx, chID, "⌛ That button is out of date: its prompt was already answered or has expired."); err != nil {
		slog.Error("failed to send stale button notice", "err", err)
	}
	if err := opts.done(ctx, chID); err != nil {
		slog.Error("failed to close turn after stale button notice", "err", err)
	}
}

// isUserMessage reports whether a message was typed or pressed by the user, which is the only
// source that may answer a prompt. A missing source is the user's, as everywhere in the loop.
func isUserMessage(msg channel.TaggedMessage) bool {
	return msg.SourceInfo == nil || msg.SourceInfo.Source == channel.SourceUser
}
