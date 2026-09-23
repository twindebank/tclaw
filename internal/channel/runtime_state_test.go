package channel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
)

func TestRuntimeStateStore_ArmPendingAction(t *testing.T) {
	t.Run("arms an idle channel", func(t *testing.T) {
		rs := newRuntimeStateStore(t)
		ctx := context.Background()

		require.NoError(t, rs.ArmPendingAction(ctx, "chan", channel.NewPendingAction(channel.PendingSecretDelete, nil)))

		state, err := rs.Get(ctx, "chan")
		require.NoError(t, err)
		require.Equal(t, channel.PendingSecretDelete, state.PendingAction.Kind)
	})

	t.Run("refuses while another confirmation is waiting", func(t *testing.T) {
		rs := newRuntimeStateStore(t)
		ctx := context.Background()
		require.NoError(t, rs.ArmPendingAction(ctx, "chan", channel.NewPendingAction(channel.PendingSecretDelete, nil)))

		// The direction that matters most: an access grant must not take over a
		// prompt the user is answering about deleting a secret, or their "yes"
		// becomes consent to the grant.
		err := rs.ArmPendingAction(ctx, "chan", channel.NewPendingAction(channel.PendingRepoGrant, nil))
		require.Error(t, err)
		require.True(t, errors.Is(err, channel.ErrConfirmationPending), "callers should be able to tell this refusal apart")

		state, getErr := rs.Get(ctx, "chan")
		require.NoError(t, getErr)
		require.Equal(t, channel.PendingSecretDelete, state.PendingAction.Kind,
			"the prompt the user can see must still be the one armed")
	})

	t.Run("replaces a confirmation that expired unanswered", func(t *testing.T) {
		rs := newRuntimeStateStore(t)
		ctx := context.Background()
		require.NoError(t, rs.Update(ctx, "chan", func(state *channel.RuntimeState) {
			expired := channel.NewPendingAction(channel.PendingRepoGrant, nil)
			expired.ExpiresAt = time.Now().Add(-time.Hour)
			state.PendingAction = expired
		}))

		// An abandoned prompt must not block the channel for good.
		require.NoError(t, rs.ArmPendingAction(ctx, "chan", channel.NewPendingAction(channel.PendingSecretDelete, nil)))

		state, err := rs.Get(ctx, "chan")
		require.NoError(t, err)
		require.Equal(t, channel.PendingSecretDelete, state.PendingAction.Kind)
	})

	t.Run("keeps each channel's confirmation to itself", func(t *testing.T) {
		rs := newRuntimeStateStore(t)
		ctx := context.Background()
		require.NoError(t, rs.ArmPendingAction(ctx, "a", channel.NewPendingAction(channel.PendingSecretDelete, nil)))

		require.NoError(t, rs.ArmPendingAction(ctx, "b", channel.NewPendingAction(channel.PendingRepoGrant, nil)),
			"a prompt waiting on one channel must not block another")
	})
}

// --- helpers ---

func newRuntimeStateStore(t *testing.T) *channel.RuntimeStateStore {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return channel.NewRuntimeStateStore(s)
}
