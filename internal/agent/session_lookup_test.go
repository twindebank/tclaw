package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
)

func TestLookupSession(t *testing.T) {
	t.Run("falls back to sessions map when no resolver", func(t *testing.T) {
		sessions := map[channel.ChannelID]string{"ch": "sess-1"}
		got := lookupSession(Options{}, sessions, "ch")
		require.Equal(t, "sess-1", got)
	})

	t.Run("resolver takes precedence over sessions map", func(t *testing.T) {
		sessions := map[channel.ChannelID]string{"ch": "stale-sess"}
		opts := Options{
			SessionResolver: func(chID channel.ChannelID) string {
				require.Equal(t, channel.ChannelID("ch"), chID)
				return "live-sess"
			},
		}
		got := lookupSession(opts, sessions, "ch")
		require.Equal(t, "live-sess", got)
	})

	t.Run("resolver returning empty forces a fresh session", func(t *testing.T) {
		// This is the timeout case: the resolver decides the persisted
		// session is too old to reuse, so the agent should start fresh
		// even though it has a session ID cached in its local map.
		sessions := map[channel.ChannelID]string{"ch": "old-sess"}
		opts := Options{
			SessionResolver: func(chID channel.ChannelID) string { return "" },
		}
		got := lookupSession(opts, sessions, "ch")
		require.Equal(t, "", got)
	})
}
