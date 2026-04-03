package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"tclaw/internal/channel"
)

// handleResetFlow processes a message within an active reset flow.
// Returns a FlowResult indicating what happened.
func handleResetFlow(
	ctx context.Context,
	opts Options,
	fm *FlowManager,
	flow *pendingReset,
	ch channel.Channel,
	msg channel.TaggedMessage,
	sessions map[channel.ChannelID]string,
) FlowResult {
	switch flow.state {
	case resetChoosing:
		return handleResetChoosing(ctx, opts, fm, flow, ch, msg, sessions)
	case resetConfirming:
		return handleResetConfirming(ctx, opts, fm, flow, ch, msg)
	default:
		slog.Error("unexpected reset state", "state", flow.state, "channel", msg.ChannelID)
		fm.Complete(msg.ChannelID)
		return FlowResult{Handled: true}
	}
}

func handleResetChoosing(
	ctx context.Context,
	opts Options,
	fm *FlowManager,
	flow *pendingReset,
	ch channel.Channel,
	msg channel.TaggedMessage,
	sessions map[channel.ChannelID]string,
) FlowResult {
	levels := allowedResetLevels(opts, msg.ChannelID)
	choice := strings.TrimSpace(strings.ToLower(msg.Text))
	chosen := resolveResetChoice(choice, levels)

	switch chosen {
	case ResetSession:
		old := sessions[msg.ChannelID]
		delete(sessions, msg.ChannelID)
		if opts.OnSessionUpdate != nil {
			opts.OnSessionUpdate(msg.ChannelID, "")
		}
		slog.Info("session reset", "channel", msg.ChannelID, "old_session", old)
		if _, err := opts.send(ctx, msg.ChannelID, "🗑️ Session cleared — next message starts a fresh conversation."); err != nil {
			slog.Error("failed to send reset confirmation", "err", err)
		}
		fm.Complete(msg.ChannelID)
		return FlowResult{Handled: true}

	case ResetMemories, ResetProject, ResetAll:
		flow.level = chosen
		flow.state = resetConfirming
		if _, err := opts.send(ctx, msg.ChannelID, resetConfirmPrompt(chosen, ch.Markup())); err != nil {
			slog.Error("failed to send reset confirm prompt", "err", err)
		}
		return FlowResult{Handled: true}

	case resetCancel:
		if _, err := opts.send(ctx, msg.ChannelID, "↩️ Reset cancelled."); err != nil {
			slog.Error("failed to send reset cancel", "err", err)
		}
		fm.Complete(msg.ChannelID)
		return FlowResult{Handled: true}

	default:
		// Invalid choice — re-prompt.
		maxN := len(levels) + 1
		if _, err := opts.send(ctx, msg.ChannelID, fmt.Sprintf("Please enter a number (1-%d).", maxN)); err != nil {
			slog.Error("failed to send reset re-prompt", "err", err)
		}
		return FlowResult{Handled: true}
	}
}

func handleResetConfirming(
	ctx context.Context,
	opts Options,
	fm *FlowManager,
	flow *pendingReset,
	ch channel.Channel,
	msg channel.TaggedMessage,
) FlowResult {
	if strings.TrimSpace(strings.ToLower(msg.Text)) != "confirm" {
		// Anything other than "confirm" cancels.
		if _, err := opts.send(ctx, msg.ChannelID, "↩️ Reset cancelled."); err != nil {
			slog.Error("failed to send reset cancel", "err", err)
		}
		fm.Complete(msg.ChannelID)
		return FlowResult{Handled: true}
	}

	slog.Info("reset confirmed", "level", resetLevelName(flow.level), "channel", msg.ChannelID)

	if opts.OnReset != nil {
		if err := opts.OnReset(flow.level); err != nil {
			slog.Error("reset failed", "level", resetLevelName(flow.level), "err", err)
			if _, sendErr := opts.send(ctx, msg.ChannelID, "❌ Reset failed: "+err.Error()); sendErr != nil {
				slog.Error("failed to send reset error", "err", sendErr)
			}
			fm.Complete(msg.ChannelID)
			if err := opts.done(ctx, msg.ChannelID); err != nil {
				slog.Error("failed to close turn after reset error", "err", err)
			}
			return FlowResult{Handled: true}
		}
	}

	levelName := resetLevelName(flow.level)
	if _, err := opts.send(ctx, msg.ChannelID, "✅ "+bold(ch.Markup(), strings.ToUpper(levelName[:1])+levelName[1:])+" reset complete."); err != nil {
		slog.Error("failed to send reset confirmation", "err", err)
	}
	fm.Complete(msg.ChannelID)

	// Project and Everything resets require the agent to restart.
	if flow.level == ResetProject || flow.level == ResetAll {
		if err := opts.done(ctx, msg.ChannelID); err != nil {
			slog.Error("failed to close turn before restart", "err", err)
		}
		return FlowResult{Handled: true, RestartAgent: ErrResetRequested}
	}

	return FlowResult{Handled: true}
}
