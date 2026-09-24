package router

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/tool/secretform"
)

// SecretDeletePayload is the secret to remove, held until the user answers.
type SecretDeletePayload struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// confirmSecretDelete removes the approved secret. It runs outside the sandbox,
// and is the only path by which the agent can cause a secret to be deleted.
func confirmSecretDelete(ctx context.Context, chID channel.ChannelID, chName string, pending channel.PendingAction, params confirmParams) bool {
	notify := func(text string) {
		if params.Notify != nil {
			params.Notify(ctx, chID, text)
		}
	}

	var payload SecretDeletePayload
	if err := json.Unmarshal(pending.Payload, &payload); err != nil {
		slog.Error("secret delete: failed to decode approved payload", "channel", chName, "err", err)
		notify("❌ Something went wrong reading that request, so nothing was deleted. Ask me again.")
		return true
	}
	// The tool checked this, but the payload has sat in storage since and this
	// runs against the real store.
	var collected map[string]bool
	if params.CollectedSecretKeys != nil {
		var err error
		if collected, err = params.CollectedSecretKeys(ctx); err != nil {
			slog.Error("secret delete: failed to read which secrets you supplied", "channel", chName, "err", err)
			notify("❌ Could not check which secrets you supplied, so nothing was deleted.")
			return true
		}
	}
	if err := secretform.ValidateDeletableKey(payload.Key, collected); err != nil {
		slog.Error("secret delete: approved key is not deletable", "channel", chName, "key", payload.Key, "err", err)
		notify(fmt.Sprintf("❌ %q cannot be deleted: %s", payload.Key, err))
		return true
	}

	if err := params.SecretStore.Delete(ctx, payload.Key); err != nil {
		slog.Error("secret delete: failed to delete", "channel", chName, "key", payload.Key, "err", err)
		notify(fmt.Sprintf("❌ Could not delete %q: %s", payload.Key, err))
		return true
	}
	if params.ForgetSecretKey != nil {
		if err := params.ForgetSecretKey(ctx, payload.Key); err != nil {
			// The secret is gone; only the record of it lingers, which would
			// let the agent offer to delete a key that no longer exists.
			slog.Error("secret delete: failed to forget the deleted key", "channel", chName, "key", payload.Key, "err", err)
		}
	}

	slog.Info("secret deleted after confirmation", "channel", chName, "key", payload.Key)
	notify(fmt.Sprintf("🗑️ Deleted the stored secret %q.", payload.Key))
	return true
}

// armSecretDeleteParams is what the armer needs to reach the user.
type armSecretDeleteParams struct {
	RuntimeState  *channel.RuntimeStateStore
	ActiveChannel func() string
	Channels      func() map[channel.ChannelID]channel.Channel
	Send          func(ctx context.Context, chID channel.ChannelID, text string, opts channel.SendOpts) (channel.MessageID, error)
	SecretStore   secret.Store
}

// newSecretDeleteArmer returns the hook secret_form_delete calls to ask the user
// to approve removing a stored secret.
func newSecretDeleteArmer(params armSecretDeleteParams) func(context.Context, secretform.DeleteRequest) error {
	return func(ctx context.Context, request secretform.DeleteRequest) error {
		chName := ""
		if params.ActiveChannel != nil {
			chName = params.ActiveChannel()
		}
		if chName == "" {
			return fmt.Errorf("no active channel to ask for confirmation on")
		}

		var chID channel.ChannelID
		for id, ch := range params.Channels() {
			if ch.Info().Name == chName {
				chID = id
				break
			}
		}
		if chID == "" {
			return fmt.Errorf("channel %q is not live, so nobody can be asked", chName)
		}

		// Asking about a key that holds nothing wastes the user's attention and
		// reads like the secret existed.
		value, err := params.SecretStore.Get(ctx, request.Key)
		if err != nil {
			return fmt.Errorf("check whether %q is set: %w", request.Key, err)
		}
		if value == "" {
			return fmt.Errorf("no secret is stored under %q, so there is nothing to delete", request.Key)
		}

		payload, err := json.Marshal(SecretDeletePayload{Key: request.Key, Reason: request.Reason})
		if err != nil {
			return fmt.Errorf("encode secret delete: %w", err)
		}

		// Armed before the prompt is sent: the other way round, a fast "yes"
		// could arrive while nothing was armed, slip past the intercept and
		// reach the agent, which would then answer its own prompt.
		if err := params.RuntimeState.ArmPendingAction(ctx, chName, channel.NewPendingAction(channel.PendingSecretDelete, payload)); err != nil {
			return fmt.Errorf("arm secret delete confirmation: %w", err)
		}

		prompt := fmt.Sprintf("🗑️ Delete the stored secret %q?\n\nWhy: %s\n\n"+
			"The value is gone for good. Setting it up again may mean a fresh login or a redeploy, "+
			"not just typing it back.\n\nReply \"yes\" to delete it.",
			request.Key, request.Reason)
		if _, err := params.Send(ctx, chID, prompt, channel.SendOpts{}); err != nil {
			// Roll back so the channel isn't left armed for a deletion the user
			// was never actually asked about.
			if clearErr := params.RuntimeState.Update(ctx, chName, func(rs *channel.RuntimeState) {
				rs.PendingAction = nil
			}); clearErr != nil {
				slog.Error("failed to disarm secret delete after prompt send failure",
					"channel", chName, "key", request.Key, "err", clearErr)
			}
			return fmt.Errorf("send confirmation prompt: %w", err)
		}

		slog.Info("secret delete confirmation sent", "key", request.Key, "channel", chName)
		return nil
	}
}
