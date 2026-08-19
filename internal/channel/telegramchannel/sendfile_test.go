package telegramchannel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestTelegram_SendFile(t *testing.T) {
	t.Run("refuses before any inbound message has set a chat", func(t *testing.T) {
		tg := NewTelegram("fake-token", "test", "desc", "", []int64{1}, TelegramOptions{})

		_, err := tg.SendFile(context.Background(), channel.SendFileParams{
			Filename: "guide.pdf",
			Content:  []byte("%PDF-1.4"),
		})

		require.Error(t, err)
		require.Equal(t,
			`telegram send file: no chat ID set — channel "test" has not received an inbound message yet`,
			err.Error())
	})

	t.Run("refuses empty content rather than uploading an empty document", func(t *testing.T) {
		tg := NewTelegram("fake-token", "test", "desc", "", []int64{1}, TelegramOptions{ChatID: 42})

		_, err := tg.SendFile(context.Background(), channel.SendFileParams{Filename: "guide.pdf"})

		require.Error(t, err)
		require.Equal(t, `telegram send file: "guide.pdf" has no content`, err.Error())
	})
}
