package channel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlatformState_RoundTrip(t *testing.T) {
	t.Run("telegram", func(t *testing.T) {
		original := TelegramPlatformState{ChatID: 999999999}
		raw, err := MarshalPlatformState(original)
		require.NoError(t, err)
		require.NotNil(t, raw)

		restored, err := UnmarshalPlatformState(raw)
		require.NoError(t, err)

		tps, ok := restored.(TelegramPlatformState)
		require.True(t, ok)
		require.Equal(t, int64(999999999), tps.ChatID)
	})

	t.Run("nil state", func(t *testing.T) {
		raw, err := MarshalPlatformState(nil)
		require.NoError(t, err)
		require.Nil(t, raw)

		restored, err := UnmarshalPlatformState(nil)
		require.NoError(t, err)
		require.Nil(t, restored)
	})
}
