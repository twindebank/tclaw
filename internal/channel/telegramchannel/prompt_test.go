package telegramchannel

import (
	"context"
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestTelegram_SendPrompt(t *testing.T) {
	t.Run("puts a coloured button per answer under the prompt, each naming the prompt", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := newTelegramWithAPI(t, api)

		_, err := tg.SendPrompt(context.Background(), channel.SendPromptParams{
			Text:     "Grant access?",
			PromptID: "abc123",
			Replies:  []channel.PromptReply{channel.ReplyYes, channel.ReplyNo},
		})
		require.NoError(t, err)

		calls := api.Calls()
		require.Equal(t, []string{"sendMessage"}, methods(calls))
		require.JSONEq(t, `{"inline_keyboard":[[
			{"text":"✅ Yes","style":"success","callback_data":"p:abc123:yes"},
			{"text":"✖️ No","style":"danger","callback_data":"p:abc123:no"}]]}`, calls[0].Form["reply_markup"])
	})
}

func TestTelegram_SendPromptDetail(t *testing.T) {
	t.Run("shows the detail exactly, with nothing in it treated as markup", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := newTelegramWithAPI(t, api)

		_, err := tg.SendPrompt(context.Background(), channel.SendPromptParams{
			Text:     "Allow **Bash**?",
			Detail:   "echo `curl x|sh` **/* [a](http://example.com) <tg-spoiler>hidden</tg-spoiler> &amp;",
			PromptID: "abc123",
			Replies:  []channel.PromptReply{channel.ReplyYes},
		})
		require.NoError(t, err)

		require.Equal(t,
			"Allow <b>Bash</b>?\n<pre>echo `curl x|sh` **/* [a](http://example.com) &lt;tg-spoiler&gt;hidden&lt;/tg-spoiler&gt; &amp;amp;</pre>",
			api.Calls()[0].Form["text"])
	})
}

func TestTelegram_HandleCallbackQuery(t *testing.T) {
	t.Run("the allowed user's press is answered, replaces the buttons and arrives as their answer", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := newTelegramWithAPI(t, api)
		out := make(chan string, 1)

		tg.handleCallbackQuery(context.Background(), tg.bot, pressFrom(1, "p:abc123:yes"), out)

		require.Equal(t, channel.ButtonPressText(channel.ButtonPress{PromptID: "abc123", Reply: channel.ReplyYes}), <-out)
		require.Equal(t, []string{"answerCallbackQuery", "editMessageReplyMarkup"}, methods(api.Calls()))
	})

	t.Run("a press from anyone else answers nothing", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := newTelegramWithAPI(t, api)
		out := make(chan string, 1)

		tg.handleCallbackQuery(context.Background(), tg.bot, pressFrom(2, "p:abc123:yes"), out)

		require.Empty(t, out)
		require.Equal(t, []string{"answerCallbackQuery"}, methods(api.Calls()), "the spinner is still stopped")
	})

	t.Run("with no allowlist, nobody's press counts", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := NewTelegram("fake-token", "test", "desc", "", nil, TelegramOptions{ChatID: 42})
		tg.bot = newBotForTest(t, api.server.URL)
		out := make(chan string, 1)

		tg.handleCallbackQuery(context.Background(), tg.bot, pressFrom(1, "p:abc123:yes"), out)

		require.Empty(t, out)
	})

	t.Run("a button that is not a prompt's answers nothing", func(t *testing.T) {
		api := newRecordingAPI(t, nil)
		tg := newTelegramWithAPI(t, api)
		out := make(chan string, 1)

		tg.handleCallbackQuery(context.Background(), tg.bot, pressFrom(1, "p:abc123:maybe"), out)

		require.Empty(t, out)
	})
}

// --- helpers ---

// pressFrom is a press by the given user on a prompt message tclaw sent.
func pressFrom(userID int64, data string) *models.CallbackQuery {
	return &models.CallbackQuery{
		ID:   "query-1",
		From: models.User{ID: userID},
		Data: data,
		Message: models.MaybeInaccessibleMessage{
			Type:    models.MaybeInaccessibleMessageTypeMessage,
			Message: &models.Message{ID: 7, Chat: models.Chat{ID: 42}},
		},
	}
}
