package channel

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlatformState_JSONRoundTrip(t *testing.T) {
	t.Run("telegram", func(t *testing.T) {
		original := NewTelegramPlatformState(830516211)

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var restored PlatformState
		require.NoError(t, json.Unmarshal(data, &restored))

		require.Equal(t, PlatformTelegram, restored.Type)
		require.NotNil(t, restored.Telegram)
		require.Equal(t, int64(830516211), restored.Telegram.ChatID)
	})

	t.Run("zero value", func(t *testing.T) {
		var ps PlatformState
		require.False(t, ps.HasPlatformState())
	})
}

func TestTeardownState_JSONRoundTrip(t *testing.T) {
	t.Run("telegram", func(t *testing.T) {
		original := NewTelegramTeardownState("tclaw_bot")

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var restored TeardownState
		require.NoError(t, json.Unmarshal(data, &restored))

		require.Equal(t, PlatformTelegram, restored.Type)
		require.NotNil(t, restored.Telegram)
		require.Equal(t, "tclaw_bot", restored.Telegram.BotUsername)
	})

	t.Run("zero value", func(t *testing.T) {
		var ts TeardownState
		require.False(t, ts.HasTeardownState())
	})
}
