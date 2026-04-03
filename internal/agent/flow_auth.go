package agent

import (
	"context"
	"log/slog"
	"strings"

	"tclaw/internal/channel"
)

// handleAuthFlow processes a message within an active auth flow.
// Returns a FlowResult indicating what happened.
func handleAuthFlow(
	ctx context.Context,
	opts Options,
	fm *FlowManager,
	flow *pendingAuth,
	ch channel.Channel,
	msg channel.TaggedMessage,
) FlowResult {
	switch flow.state {
	case authChoosing:
		return handleAuthChoosing(ctx, opts, fm, flow, ch, msg)
	case authOAuthActive:
		return handleAuthOAuthActive(ctx, opts, fm, flow, ch, msg)
	case authDeployConfirm:
		return handleAuthDeployConfirm(ctx, opts, fm, flow, ch, msg)
	case authAPIKeyEntry:
		return handleAuthAPIKeyEntry(ctx, opts, fm, flow, ch, msg)
	default:
		slog.Error("unexpected auth state", "state", flow.state, "channel", msg.ChannelID)
		fm.Complete(msg.ChannelID)
		return FlowResult{Handled: true}
	}
}

func handleAuthChoosing(
	ctx context.Context,
	opts Options,
	fm *FlowManager,
	flow *pendingAuth,
	ch channel.Channel,
	msg channel.TaggedMessage,
) FlowResult {
	choice := strings.TrimSpace(strings.ToLower(msg.Text))
	m := ch.Markup()

	switch choice {
	case "1", "oauth":
		if !opts.Env.IsLocal() {
			if _, err := opts.send(ctx, msg.ChannelID, "❌ OAuth login requires a browser and only works locally.\n"+
				"Use option "+bold(m, "2")+" to paste an API key instead.\n\n"+authPrompt(m)); err != nil {
				slog.Error("failed to send non-local oauth error", "err", err)
			}
		} else {
			if _, err := opts.send(ctx, msg.ChannelID, "⏳ Opening browser for OAuth login..."); err != nil {
				slog.Error("failed to send oauth starting message", "err", err)
			}
			startSetupToken(ctx, opts, flow, msg.ChannelID, fm.OAuthNotify)
		}

	case "2", "api", "key", "api key", "apikey":
		flow.state = authAPIKeyEntry
		if _, err := opts.send(ctx, msg.ChannelID, apiKeyPrompt(ch.Markup())); err != nil {
			slog.Error("failed to send api key prompt", "err", err)
		}

	case "3", "cancel":
		fm.Complete(msg.ChannelID)

	default:
		if _, err := opts.send(ctx, msg.ChannelID, "Please enter "+bold(m, "1")+" (OAuth) or "+bold(m, "2")+" (API key).\n\n"+authPrompt(m)); err != nil {
			slog.Error("failed to send auth re-prompt", "err", err)
		}
	}

	return FlowResult{Handled: true}
}

func handleAuthOAuthActive(
	ctx context.Context,
	opts Options,
	fm *FlowManager,
	flow *pendingAuth,
	ch channel.Channel,
	msg channel.TaggedMessage,
) FlowResult {
	select {
	case result := <-flow.oauthDone:
		m := ch.Markup()
		if result.setupToken != "" {
			// Persist locally so the token survives agent restarts.
			opts.SetupToken = result.setupToken
			if err := persistSetupToken(ctx, opts, result.setupToken); err != nil {
				slog.Error("failed to persist setup token", "err", err)
			}

			if opts.HasProdConfig {
				// Prod config exists — ask whether to deploy.
				flow.setupToken = result.setupToken
				flow.state = authDeployConfirm
				if _, err := opts.send(ctx, msg.ChannelID, "✅ "+result.loginMessage+"\n\n"+
					"Deploy setup token to production? Reply "+bold(m, "yes")+" or "+bold(m, "no")+"."); err != nil {
					slog.Error("failed to send deploy prompt", "err", err)
				}
			} else {
				// No prod config — skip deploy prompt, retry original message.
				if _, err := opts.send(ctx, msg.ChannelID, "✅ "+result.loginMessage); err != nil {
					slog.Error("failed to send oauth success", "err", err)
				}
				retryMsg := flow.originalMsg
				fm.Complete(msg.ChannelID)
				return retryResult(retryMsg)
			}
		} else {
			if _, err := opts.send(ctx, msg.ChannelID, "❌ "+result.loginMessage); err != nil {
				slog.Error("failed to send oauth failure", "err", err)
			}
			fm.Complete(msg.ChannelID)
		}

	default:
		// OAuth still running.
		if _, err := opts.send(ctx, msg.ChannelID, "⏳ Still authenticating in browser. Send a message after you're done."); err != nil {
			slog.Error("failed to send oauth wait message", "err", err)
		}
	}

	return FlowResult{Handled: true}
}

func handleAuthDeployConfirm(
	ctx context.Context,
	opts Options,
	fm *FlowManager,
	flow *pendingAuth,
	ch channel.Channel,
	msg channel.TaggedMessage,
) FlowResult {
	answer := strings.TrimSpace(strings.ToLower(msg.Text))
	m := ch.Markup()
	slog.Info("deploy confirm received", "answer", answer, "user_id", opts.UserID, "token_len", len(flow.setupToken))

	switch answer {
	case "yes", "y":
		if _, sendErr := opts.send(ctx, msg.ChannelID, "⏳ Deploying to production..."); sendErr != nil {
			slog.Error("failed to send deploy progress", "err", sendErr)
		}
		slog.Info("deploying setup token", "user_id", opts.UserID)
		if err := deploySetupToken(ctx, opts.UserID, flow.setupToken); err != nil {
			slog.Error("failed to deploy setup token", "err", err)
			if _, sendErr := opts.send(ctx, msg.ChannelID, "❌ Deploy failed: "+err.Error()); sendErr != nil {
				slog.Error("failed to send deploy error", "err", sendErr)
			}
		} else {
			if _, sendErr := opts.send(ctx, msg.ChannelID, "✅ Deployed to production."); sendErr != nil {
				slog.Error("failed to send deploy success", "err", sendErr)
			}
		}
		retryMsg := flow.originalMsg
		fm.Complete(msg.ChannelID)
		return retryResult(retryMsg)

	case "no", "n", "skip":
		if _, sendErr := opts.send(ctx, msg.ChannelID, "✅ Skipped deploy. Token saved locally."); sendErr != nil {
			slog.Error("failed to send skip confirmation", "err", sendErr)
		}
		retryMsg := flow.originalMsg
		fm.Complete(msg.ChannelID)
		return retryResult(retryMsg)

	default:
		if _, err := opts.send(ctx, msg.ChannelID, "Reply "+bold(m, "yes")+" to deploy or "+bold(m, "no")+" to skip."); err != nil {
			slog.Error("failed to send deploy re-prompt", "err", err)
		}
		return FlowResult{Handled: true}
	}
}

func handleAuthAPIKeyEntry(
	ctx context.Context,
	opts Options,
	fm *FlowManager,
	flow *pendingAuth,
	ch channel.Channel,
	msg channel.TaggedMessage,
) FlowResult {
	success := handleAPIKeyEntry(ctx, opts, ch, msg.ChannelID, msg.Text)
	if success {
		opts.APIKey = strings.TrimSpace(msg.Text)
		retryMsg := flow.originalMsg
		fm.Complete(msg.ChannelID)
		return retryResult(retryMsg)
	}
	return FlowResult{Handled: true}
}

// retryResult builds a FlowResult that retries the original message.
func retryResult(originalMsg channel.TaggedMessage) FlowResult {
	result := FlowResult{Handled: true}
	if originalMsg.Text != "" {
		result.RetryMessages = []channel.TaggedMessage{originalMsg}
	}
	return result
}
