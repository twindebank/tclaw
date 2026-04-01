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
			if _, err := ch.Send(ctx, "❌ OAuth login requires a browser and only works locally.\n"+
				"Use option "+bold(m, "2")+" to paste an API key instead.\n\n"+authPrompt(m)); err != nil {
				slog.Error("failed to send non-local oauth error", "err", err)
			}
		} else {
			if _, err := ch.Send(ctx, "⏳ Opening browser for OAuth login..."); err != nil {
				slog.Error("failed to send oauth starting message", "err", err)
			}
			startSetupToken(ctx, opts, flow, msg.ChannelID, fm.OAuthNotify)
		}

	case "2", "api", "key", "api key", "apikey":
		flow.state = authAPIKeyEntry
		if _, err := ch.Send(ctx, apiKeyPrompt(ch.Markup())); err != nil {
			slog.Error("failed to send api key prompt", "err", err)
		}

	case "3", "cancel":
		fm.Complete(msg.ChannelID)

	default:
		if _, err := ch.Send(ctx, "Please enter "+bold(m, "1")+" (OAuth) or "+bold(m, "2")+" (API key).\n\n"+authPrompt(m)); err != nil {
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
				if _, err := ch.Send(ctx, "✅ "+result.loginMessage+"\n\n"+
					"Deploy setup token to production? Reply "+bold(m, "yes")+" or "+bold(m, "no")+"."); err != nil {
					slog.Error("failed to send deploy prompt", "err", err)
				}
			} else {
				// No prod config — skip deploy prompt, retry original message.
				if _, err := ch.Send(ctx, "✅ "+result.loginMessage); err != nil {
					slog.Error("failed to send oauth success", "err", err)
				}
				retryMsg := flow.originalMsg
				fm.Complete(msg.ChannelID)
				return retryResult(retryMsg)
			}
		} else {
			if _, err := ch.Send(ctx, "❌ "+result.loginMessage); err != nil {
				slog.Error("failed to send oauth failure", "err", err)
			}
			fm.Complete(msg.ChannelID)
		}

	default:
		// OAuth still running.
		if _, err := ch.Send(ctx, "⏳ Still authenticating in browser. Send a message after you're done."); err != nil {
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
		if _, sendErr := ch.Send(ctx, "⏳ Deploying to production..."); sendErr != nil {
			slog.Error("failed to send deploy progress", "err", sendErr)
		}
		slog.Info("deploying setup token", "user_id", opts.UserID)
		if err := deploySetupToken(ctx, opts.UserID, flow.setupToken); err != nil {
			slog.Error("failed to deploy setup token", "err", err)
			if _, sendErr := ch.Send(ctx, "❌ Deploy failed: "+err.Error()); sendErr != nil {
				slog.Error("failed to send deploy error", "err", sendErr)
			}
		} else {
			if _, sendErr := ch.Send(ctx, "✅ Deployed to production."); sendErr != nil {
				slog.Error("failed to send deploy success", "err", sendErr)
			}
		}
		retryMsg := flow.originalMsg
		fm.Complete(msg.ChannelID)
		return retryResult(retryMsg)

	case "no", "n", "skip":
		if _, sendErr := ch.Send(ctx, "✅ Skipped deploy. Token saved locally."); sendErr != nil {
			slog.Error("failed to send skip confirmation", "err", sendErr)
		}
		retryMsg := flow.originalMsg
		fm.Complete(msg.ChannelID)
		return retryResult(retryMsg)

	default:
		if _, err := ch.Send(ctx, "Reply "+bold(m, "yes")+" to deploy or "+bold(m, "no")+" to skip."); err != nil {
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
	success := handleAPIKeyEntry(ctx, opts, ch, msg.Text)
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
