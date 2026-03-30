package telegramchannel_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/channel/telegramchannel"
)

func TestTelegramPlatformState_RoundTrip(t *testing.T) {
	original := telegramchannel.NewPlatformState(999999999)

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored channel.PlatformState
	require.NoError(t, json.Unmarshal(data, &restored))

	require.Equal(t, channel.PlatformTelegram, restored.Type)

	parsed, err := telegramchannel.ParsePlatformState(restored)
	require.NoError(t, err)
	require.Equal(t, int64(999999999), parsed.ChatID)
}

func TestTelegramTeardownState_RoundTrip(t *testing.T) {
	original := telegramchannel.NewTeardownState("tclaw_bot")

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored channel.TeardownState
	require.NoError(t, json.Unmarshal(data, &restored))

	require.Equal(t, channel.PlatformTelegram, restored.Type)

	parsed, err := telegramchannel.ParseTeardownState(restored)
	require.NoError(t, err)
	require.Equal(t, "tclaw_bot", parsed.BotUsername)
}
