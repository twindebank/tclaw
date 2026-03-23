package channel_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
)

func TestTeardownState_RoundTrip(t *testing.T) {
	t.Run("telegram state", func(t *testing.T) {
		original := channel.TelegramTeardownState{BotUsername: "tclaw_a3f7b21e_bot"}

		raw, err := channel.MarshalTeardownState(original)
		require.NoError(t, err)
		require.NotNil(t, raw)

		// Verify the envelope structure.
		var envelope map[string]any
		require.NoError(t, json.Unmarshal(raw, &envelope))
		require.Equal(t, "telegram", envelope["type"])

		// Round-trip back.
		restored, err := channel.UnmarshalTeardownState(raw)
		require.NoError(t, err)

		tg, ok := restored.(channel.TelegramTeardownState)
		require.True(t, ok, "expected TelegramTeardownState, got %T", restored)
		require.Equal(t, "tclaw_a3f7b21e_bot", tg.BotUsername)
	})

	t.Run("nil state", func(t *testing.T) {
		raw, err := channel.MarshalTeardownState(nil)
		require.NoError(t, err)
		require.Nil(t, raw)

		restored, err := channel.UnmarshalTeardownState(nil)
		require.NoError(t, err)
		require.Nil(t, restored)
	})

	t.Run("null JSON", func(t *testing.T) {
		restored, err := channel.UnmarshalTeardownState(json.RawMessage("null"))
		require.NoError(t, err)
		require.Nil(t, restored)
	})

	t.Run("unknown type returns error", func(t *testing.T) {
		raw := json.RawMessage(`{"type":"slack","data":{}}`)
		_, err := channel.UnmarshalTeardownState(raw)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown teardown state type")
	})
}

func TestDynamicChannelConfig_TeardownStateJSON(t *testing.T) {
	t.Run("config with teardown state round-trips through JSON", func(t *testing.T) {
		original := channel.DynamicChannelConfig{
			Name:      "ephemeral-test",
			Type:      channel.TypeTelegram,
			Ephemeral: true,
			TeardownState: channel.TelegramTeardownState{
				BotUsername: "tclaw_deadbeef_bot",
			},
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var restored channel.DynamicChannelConfig
		require.NoError(t, json.Unmarshal(data, &restored))

		require.Equal(t, "ephemeral-test", restored.Name)
		require.True(t, restored.Ephemeral)
		require.NotNil(t, restored.TeardownState)

		tg, ok := restored.TeardownState.(channel.TelegramTeardownState)
		require.True(t, ok)
		require.Equal(t, "tclaw_deadbeef_bot", tg.BotUsername)
	})

	t.Run("config without teardown state round-trips cleanly", func(t *testing.T) {
		original := channel.DynamicChannelConfig{
			Name: "normal-channel",
			Type: channel.TypeSocket,
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var restored channel.DynamicChannelConfig
		require.NoError(t, json.Unmarshal(data, &restored))

		require.Equal(t, "normal-channel", restored.Name)
		require.Nil(t, restored.TeardownState)
	})
}
