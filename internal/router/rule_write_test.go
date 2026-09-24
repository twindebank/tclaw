package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/tool/ruletools"
)

func TestNewRuleWriteArmer(t *testing.T) {
	t.Run("refuses while another confirmation is waiting, and leaves it alone", func(t *testing.T) {
		rs, _, _ := setupDoneTest(t)
		first := channel.NewPendingAction(channel.PendingRepoGrant, nil)
		require.NoError(t, rs.Update(context.Background(), "dev", func(s *channel.RuntimeState) {
			s.PendingAction = first
		}))
		sent := 0
		arm := newRuleWriteArmer(armRuleWriteParams{
			RuntimeState:  rs,
			ActiveChannel: func() string { return "dev" },
			Channels: func() map[channel.ChannelID]channel.Channel {
				return map[channel.ChannelID]channel.Channel{"dev-id": &stubDoneChannel{info: channel.Info{Name: "dev"}}}
			},
			Send: func(context.Context, channel.ChannelID, string, channel.SendOpts) (channel.MessageID, error) {
				sent++
				return "1", nil
			},
		})

		err := arm(context.Background(), ruletools.RuleWriteRequest{File: "example.md", Content: "rule", Reason: "why"})

		require.ErrorIs(t, err, channel.ErrPromptWaiting)
		require.Zero(t, sent, "no second prompt is sent")
		state, err := rs.Get(context.Background(), "dev")
		require.NoError(t, err)
		require.Equal(t, first.PromptID, state.PendingAction.PromptID)
	})
}
