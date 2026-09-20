package telegram

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

func TestParseBotUsernames(t *testing.T) {
	t.Run("extracts bot usernames, strips the @, skips control buttons and duplicates", func(t *testing.T) {
		msg := &tg.Message{
			ReplyMarkup: &tg.ReplyInlineMarkup{
				Rows: []tg.KeyboardButtonRow{
					{Buttons: []tg.KeyboardButtonClass{
						&tg.KeyboardButtonCallback{Text: "@tclaw_ab12cd34_bot"},
						&tg.KeyboardButtonCallback{Text: "@tclaw_ef56gh78_bot"},
					}},
					{Buttons: []tg.KeyboardButtonClass{
						&tg.KeyboardButtonCallback{Text: "« Back"},
						&tg.KeyboardButtonCallback{Text: "@tclaw_ab12cd34_bot"},
					}},
				},
			},
		}

		require.Equal(t, []string{"tclaw_ab12cd34_bot", "tclaw_ef56gh78_bot"}, parseBotUsernames(msg))
	})

	t.Run("returns nil when the message has no inline keyboard", func(t *testing.T) {
		require.Nil(t, parseBotUsernames(&tg.Message{Message: "Choose a bot from the list below:"}))
	})
}
