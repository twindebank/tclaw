package channel_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestArmPendingAction(t *testing.T) {
	now := time.Now()

	t.Run("arms when nothing is waiting", func(t *testing.T) {
		rs := &channel.RuntimeState{}
		pending := channel.NewPendingAction(channel.PendingRuleWrite, json.RawMessage(`{}`))

		require.NoError(t, channel.ArmPendingAction(rs, pending, now))
		require.Equal(t, pending, rs.PendingAction)
	})

	t.Run("refuses to replace a prompt the user has not answered", func(t *testing.T) {
		first := channel.NewPendingAction(channel.PendingRepoGrant, nil)
		rs := &channel.RuntimeState{PendingAction: first}

		err := channel.ArmPendingAction(rs, channel.NewPendingAction(channel.PendingRuleWrite, nil), now)

		require.ErrorIs(t, err, channel.ErrPromptWaiting)
		require.Equal(t, first, rs.PendingAction, "the question under the user stays the same")
	})

	t.Run("replaces one that has expired", func(t *testing.T) {
		rs := &channel.RuntimeState{PendingAction: channel.NewPendingAction(channel.PendingRepoGrant, nil)}
		pending := channel.NewPendingAction(channel.PendingRuleWrite, nil)

		require.NoError(t, channel.ArmPendingAction(rs, pending, now.Add(channel.PendingConfirmationTTL+time.Minute)))
		require.Equal(t, pending, rs.PendingAction)
	})
}
