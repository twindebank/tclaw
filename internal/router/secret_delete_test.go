package router

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/tool/secretform"
)

func TestConfirmSecretDelete(t *testing.T) {
	t.Run("deletes the secret on yes", func(t *testing.T) {
		rs, ss, _ := setupDoneTest(t)
		ctx := context.Background()
		require.NoError(t, ss.Set(ctx, "old_api_token", "value"))
		armSecretDelete(t, rs, "mychan", "old_api_token", "replaced")

		var notified string
		var forgotten string
		consumed := interceptPendingConfirmation(ctx, doneTaggedMsg("mychan-id", "yes"), confirmParams{
			ChannelsFunc:        doneChannelsFunc("mychan-id", "mychan", channel.TypeSocket),
			RuntimeState:        rs,
			SecretStore:         ss,
			CollectedSecretKeys: collectedFunc("old_api_token"),
			ForgetSecretKey:     func(_ context.Context, key string) error { forgotten = key; return nil },
			Notify:              func(_ context.Context, _ channel.ChannelID, text string) { notified = text },
		})
		require.True(t, consumed)

		got, err := ss.Get(ctx, "old_api_token")
		require.NoError(t, err)
		require.Empty(t, got, "the secret should be gone once approved")
		require.Contains(t, notified, "old_api_token")
		require.Equal(t, "old_api_token", forgotten,
			"the record of it must go too, or the agent can offer to delete a key that no longer exists")
	})

	t.Run("refuses a key the user never supplied", func(t *testing.T) {
		rs, ss, _ := setupDoneTest(t)
		ctx := context.Background()
		require.NoError(t, ss.Set(ctx, "telegram_client_session", "value"))
		armSecretDelete(t, rs, "mychan", "telegram_client_session", "tidying")

		var notified string
		consumed := interceptPendingConfirmation(ctx, doneTaggedMsg("mychan-id", "yes"), confirmParams{
			ChannelsFunc:        doneChannelsFunc("mychan-id", "mychan", channel.TypeSocket),
			RuntimeState:        rs,
			SecretStore:         ss,
			CollectedSecretKeys: collectedFunc("old_api_token"),
			Notify:              func(_ context.Context, _ channel.ChannelID, text string) { notified = text },
		})
		require.True(t, consumed)

		got, err := ss.Get(ctx, "telegram_client_session")
		require.NoError(t, err)
		require.Equal(t, "value", got, "an approved key the user never supplied must survive")
		require.Contains(t, notified, "cannot be deleted")
	})

	t.Run("refuses when the allowlist cannot be read", func(t *testing.T) {
		rs, ss, _ := setupDoneTest(t)
		ctx := context.Background()
		require.NoError(t, ss.Set(ctx, "old_api_token", "value"))
		armSecretDelete(t, rs, "mychan", "old_api_token", "replaced")

		consumed := interceptPendingConfirmation(ctx, doneTaggedMsg("mychan-id", "yes"), confirmParams{
			ChannelsFunc: doneChannelsFunc("mychan-id", "mychan", channel.TypeSocket),
			RuntimeState: rs,
			SecretStore:  ss,
			Notify:       func(_ context.Context, _ channel.ChannelID, _ string) {},
		})
		require.True(t, consumed)

		got, err := ss.Get(ctx, "old_api_token")
		require.NoError(t, err)
		require.Equal(t, "value", got,
			"with no allowlist the second-line check must refuse, not wave the deletion through")
	})

	t.Run("leaves the secret alone on no", func(t *testing.T) {
		rs, ss, _ := setupDoneTest(t)
		ctx := context.Background()
		require.NoError(t, ss.Set(ctx, "old_api_token", "value"))
		armSecretDelete(t, rs, "mychan", "old_api_token", "replaced")

		consumed := interceptPendingConfirmation(ctx, doneTaggedMsg("mychan-id", "no"), confirmParams{
			ChannelsFunc:        doneChannelsFunc("mychan-id", "mychan", channel.TypeSocket),
			RuntimeState:        rs,
			SecretStore:         ss,
			CollectedSecretKeys: collectedFunc("old_api_token"),
			Notify:              func(_ context.Context, _ channel.ChannelID, _ string) {},
		})
		require.False(t, consumed, "a decline is forwarded to the agent rather than swallowed")

		got, err := ss.Get(ctx, "old_api_token")
		require.NoError(t, err)
		require.Equal(t, "value", got, "declining must not delete anything")

		state, err := rs.Get(ctx, "mychan")
		require.NoError(t, err)
		require.Nil(t, state.PendingAction, "a declined prompt must not stay armed")
	})

	t.Run("refuses a key that stopped being deletable while it waited", func(t *testing.T) {
		rs, ss, _ := setupDoneTest(t)
		ctx := context.Background()
		// A payload that has sat in storage is re-checked, because this runs
		// against the real store rather than the tool's validated arguments.
		armSecretDelete(t, rs, "mychan", "cred/git/default/token", "tidying")

		var notified string
		consumed := interceptPendingConfirmation(ctx, doneTaggedMsg("mychan-id", "yes"), confirmParams{
			ChannelsFunc:        doneChannelsFunc("mychan-id", "mychan", channel.TypeSocket),
			RuntimeState:        rs,
			SecretStore:         ss,
			CollectedSecretKeys: collectedFunc("cred/git/default/token"),
			Notify:              func(_ context.Context, _ channel.ChannelID, text string) { notified = text },
		})
		require.True(t, consumed)
		require.Contains(t, notified, "cannot be deleted")
	})
}

func TestNewSecretDeleteArmer(t *testing.T) {
	t.Run("arms the channel and sends the prompt", func(t *testing.T) {
		rs, ss, _ := setupDoneTest(t)
		ctx := context.Background()
		require.NoError(t, ss.Set(ctx, "old_api_token", "value"))
		var sent string
		arm := newSecretDeleteArmer(armerParams(rs, ss, func(text string) error { sent = text; return nil }))

		require.NoError(t, arm(ctx, secretform.DeleteRequest{Key: "old_api_token", Reason: "replaced"}))

		require.Contains(t, sent, "old_api_token")
		require.Contains(t, sent, "replaced")
		state, err := rs.Get(ctx, "mychan")
		require.NoError(t, err)
		require.NotNil(t, state.PendingAction)
		require.Equal(t, channel.PendingSecretDelete, state.PendingAction.Kind)
	})

	t.Run("refuses a key that holds nothing", func(t *testing.T) {
		rs, ss, _ := setupDoneTest(t)
		ctx := context.Background()
		arm := newSecretDeleteArmer(armerParams(rs, ss, func(string) error { return nil }))

		err := arm(ctx, secretform.DeleteRequest{Key: "ghost", Reason: "tidying"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "nothing to delete")

		state, getErr := rs.Get(ctx, "mychan")
		require.NoError(t, getErr)
		require.Nil(t, state.PendingAction, "a refused request must not arm the channel")
	})

	t.Run("disarms when the prompt cannot be sent", func(t *testing.T) {
		rs, ss, _ := setupDoneTest(t)
		ctx := context.Background()
		require.NoError(t, ss.Set(ctx, "old_api_token", "value"))
		arm := newSecretDeleteArmer(armerParams(rs, ss, func(string) error {
			return fmt.Errorf("transport down")
		}))

		err := arm(ctx, secretform.DeleteRequest{Key: "old_api_token", Reason: "replaced"})
		require.Error(t, err)

		state, getErr := rs.Get(ctx, "mychan")
		require.NoError(t, getErr)
		require.Nil(t, state.PendingAction,
			"a prompt the user never saw must not leave the channel armed")
	})

	t.Run("refuses while another confirmation is waiting", func(t *testing.T) {
		rs, ss, _ := setupDoneTest(t)
		ctx := context.Background()
		require.NoError(t, ss.Set(ctx, "old_api_token", "value"))
		require.NoError(t, rs.Update(ctx, "mychan", func(state *channel.RuntimeState) {
			state.PendingAction = channel.NewPendingAction(channel.PendingRepoGrant, nil)
		}))
		arm := newSecretDeleteArmer(armerParams(rs, ss, func(string) error { return nil }))

		err := arm(ctx, secretform.DeleteRequest{Key: "old_api_token", Reason: "replaced"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "already waiting")
		state, getErr := rs.Get(ctx, "mychan")
		require.NoError(t, getErr)
		require.Equal(t, channel.PendingRepoGrant, state.PendingAction.Kind,
			"the user's answer to the live prompt must not become a deletion")
	})

	t.Run("proceeds when the waiting confirmation has expired", func(t *testing.T) {
		rs, ss, _ := setupDoneTest(t)
		ctx := context.Background()
		require.NoError(t, ss.Set(ctx, "old_api_token", "value"))
		// An abandoned prompt must not wedge the channel for good.
		require.NoError(t, rs.Update(ctx, "mychan", func(state *channel.RuntimeState) {
			expired := channel.NewPendingAction(channel.PendingRepoGrant, nil)
			expired.ExpiresAt = time.Now().Add(-time.Hour)
			state.PendingAction = expired
		}))
		arm := newSecretDeleteArmer(armerParams(rs, ss, func(string) error { return nil }))

		require.NoError(t, arm(ctx, secretform.DeleteRequest{Key: "old_api_token", Reason: "replaced"}))

		state, getErr := rs.Get(ctx, "mychan")
		require.NoError(t, getErr)
		require.Equal(t, channel.PendingSecretDelete, state.PendingAction.Kind,
			"an expired prompt nobody answered should be replaced, not respected")
	})
}

// --- helpers ---

// collectedFunc reports the given keys as ones the user supplied through a form.
func collectedFunc(keys ...string) func(context.Context) (map[string]bool, error) {
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return func(context.Context) (map[string]bool, error) { return set, nil }
}

// armerParams wires the armer to one live channel, with send standing in for the
// transport so a failure can be provoked.
func armerParams(rs *channel.RuntimeStateStore, ss secret.Store, send func(string) error) armSecretDeleteParams {
	return armSecretDeleteParams{
		RuntimeState:  rs,
		ActiveChannel: func() string { return "mychan" },
		Channels:      doneChannelsFunc("mychan-id", "mychan", channel.TypeSocket),
		Send: func(_ context.Context, _ channel.ChannelID, text string, _ channel.SendOpts) (channel.MessageID, error) {
			if err := send(text); err != nil {
				return "", err
			}
			return "msg-1", nil
		},
		SecretStore: ss,
	}
}

func armSecretDelete(t *testing.T, rs *channel.RuntimeStateStore, chName, key, reason string) {
	t.Helper()
	payload, err := json.Marshal(SecretDeletePayload{Key: key, Reason: reason})
	require.NoError(t, err)
	require.NoError(t, rs.Update(context.Background(), chName, func(state *channel.RuntimeState) {
		state.PendingAction = channel.NewPendingAction(channel.PendingSecretDelete, payload)
	}))
}
