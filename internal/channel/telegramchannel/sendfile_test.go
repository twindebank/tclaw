package telegramchannel

import (
	"context"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

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

func TestTrimToUTF16Units(t *testing.T) {
	t.Run("leaves a caption within the limit alone", func(t *testing.T) {
		trimmed, cut := trimToUTF16Units("a short caption", 1024)

		require.False(t, cut, "nothing to cut")
		require.Equal(t, "a short caption", trimmed)
	})

	t.Run("counts an emoji as the two units Telegram counts it as", func(t *testing.T) {
		// 600 emoji is 600 runes but 1200 UTF-16 units, so it must be cut
		trimmed, cut := trimToUTF16Units(strings.Repeat("🐈", 600), 1024)

		require.True(t, cut, "a rune count would have let this through")
		require.LessOrEqual(t, len(utf16.Encode([]rune(trimmed))), 1024, "the result fits the real limit")
		require.True(t, strings.HasSuffix(trimmed, "…"), "the cut is marked")
	})

	t.Run("cuts plain text on a character boundary", func(t *testing.T) {
		trimmed, cut := trimToUTF16Units(strings.Repeat("a", 2000), 1024)

		require.True(t, cut)
		require.LessOrEqual(t, len(utf16.Encode([]rune(trimmed))), 1024)
		require.True(t, utf8.ValidString(trimmed), "no character is split in half")
	})
}
