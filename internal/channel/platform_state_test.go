package channel

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlatformState_JSONRoundTrip(t *testing.T) {
	t.Run("round trip with data", func(t *testing.T) {
		original := NewPlatformState(PlatformTelegram, map[string]int64{"chat_id": 999999999})

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var restored PlatformState
		require.NoError(t, json.Unmarshal(data, &restored))

		require.Equal(t, PlatformTelegram, restored.Type)
		require.True(t, restored.HasPlatformState())

		var parsed map[string]int64
		require.NoError(t, restored.ParsePlatformData(&parsed))
		require.Equal(t, int64(999999999), parsed["chat_id"])
	})

	t.Run("zero value", func(t *testing.T) {
		var ps PlatformState
		require.False(t, ps.HasPlatformState())
	})
}

func TestTeardownState_JSONRoundTrip(t *testing.T) {
	t.Run("round trip with data", func(t *testing.T) {
		original := NewTeardownState(PlatformTelegram, map[string]string{"bot_username": "tclaw_bot"})

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var restored TeardownState
		require.NoError(t, json.Unmarshal(data, &restored))

		require.Equal(t, PlatformTelegram, restored.Type)
		require.True(t, restored.HasTeardownState())

		var parsed map[string]string
		require.NoError(t, restored.ParseTeardownData(&parsed))
		require.Equal(t, "tclaw_bot", parsed["bot_username"])
	})

	t.Run("zero value", func(t *testing.T) {
		var ts TeardownState
		require.False(t, ts.HasTeardownState())
	})
}
