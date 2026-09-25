package channel_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestParseButtonPress(t *testing.T) {
	t.Run("reads back the press a transport delivered", func(t *testing.T) {
		press := channel.ButtonPress{PromptID: "abc123", Reply: channel.ReplyNo}

		require.Equal(t, &press, channel.ParseButtonPress(channel.ButtonPressText(press)))
	})

	t.Run("is nil for an ordinary message", func(t *testing.T) {
		require.Nil(t, channel.ParseButtonPress("yes"))
		require.Nil(t, channel.ParseButtonPress("🔘 yes but no prompt"))
	})
}
