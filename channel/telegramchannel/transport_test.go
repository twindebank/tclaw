package telegramchannel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTelegram_ChatIDSeededFromOpts(t *testing.T) {
	tg := NewTelegram("fake-token", "test", "desc", []int64{1}, TelegramOptions{
		ChatID: 42,
	})
	require.Equal(t, int64(42), tg.currentChatID)
}

func TestTelegram_ChatIDDefaultsToZero(t *testing.T) {
	tg := NewTelegram("fake-token", "test", "desc", []int64{1}, TelegramOptions{})
	require.Equal(t, int64(0), tg.currentChatID)
}
