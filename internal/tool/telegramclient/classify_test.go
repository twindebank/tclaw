package telegramclient

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyBots(t *testing.T) {
	t.Run("labels a bot with the channel that claims it", func(t *testing.T) {
		channelByBot := map[string]string{"tclaw_ab12cd34_bot": "email"}

		got := classifyBots([]string{"tclaw_ab12cd34_bot"}, channelByBot)

		require.Equal(t, []botInfo{{Username: "tclaw_ab12cd34_bot", BacksChannel: "email", Orphan: false}}, got)
	})

	t.Run("flags an unclaimed auto-provisioned bot as an orphan", func(t *testing.T) {
		got := classifyBots([]string{"tclaw_ab12cd34_bot"}, map[string]string{})

		require.Equal(t, []botInfo{{Username: "tclaw_ab12cd34_bot", BacksChannel: "", Orphan: true}}, got)
	})

	t.Run("leaves an unclaimed custom bot alone", func(t *testing.T) {
		got := classifyBots([]string{"theo_assistant_bot"}, map[string]string{})

		require.Equal(t, []botInfo{{Username: "theo_assistant_bot", BacksChannel: "", Orphan: false}}, got)
	})

	t.Run("matches usernames case-insensitively against the channel map", func(t *testing.T) {
		channelByBot := map[string]string{"tclaw_ab12cd34_bot": "email"}

		got := classifyBots([]string{"TCLAW_AB12CD34_BOT"}, channelByBot)

		require.Equal(t, "email", got[0].BacksChannel)
		require.False(t, got[0].Orphan)
	})

	t.Run("classifies a mixed list of live, orphan, and custom bots", func(t *testing.T) {
		channelByBot := map[string]string{"tclaw_11111111_bot": "email"}

		got := classifyBots(
			[]string{"tclaw_11111111_bot", "tclaw_22222222_bot", "theo_admin_bot"},
			channelByBot,
		)

		require.Equal(t, []botInfo{
			{Username: "tclaw_11111111_bot", BacksChannel: "email", Orphan: false},
			{Username: "tclaw_22222222_bot", BacksChannel: "", Orphan: true},
			{Username: "theo_admin_bot", BacksChannel: "", Orphan: false},
		}, got)
	})
}
