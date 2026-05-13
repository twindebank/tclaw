package agent

import (
	"context"
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

func TestNotifyFreshSessionIfTimedOut(t *testing.T) {
	t.Run("sends notice when prior session is dropped by resolver", func(t *testing.T) {
		ch := &mockChannel{info: channel.Info{ID: "test-ch", Name: "test", Type: channel.TypeSocket}}
		opts := Options{Channels: map[channel.ChannelID]channel.Channel{"test-ch": ch}}
		sessions := map[channel.ChannelID]string{"test-ch": "old-sess"}

		notifyFreshSessionIfTimedOut(context.Background(), opts, sessions, "test-ch", "")

		require.Equal(t, []string{freshSessionNotice}, ch.sends)
		_, stillCached := sessions["test-ch"]
		require.False(t, stillCached, "local sessions entry should be cleared so retries don't resend")
	})

	t.Run("silent when no prior session existed", func(t *testing.T) {
		ch := &mockChannel{info: channel.Info{ID: "test-ch", Name: "test", Type: channel.TypeSocket}}
		opts := Options{Channels: map[channel.ChannelID]channel.Channel{"test-ch": ch}}
		sessions := map[channel.ChannelID]string{}

		notifyFreshSessionIfTimedOut(context.Background(), opts, sessions, "test-ch", "")

		require.Empty(t, ch.sends)
	})

	t.Run("silent when resolver returned a session", func(t *testing.T) {
		ch := &mockChannel{info: channel.Info{ID: "test-ch", Name: "test", Type: channel.TypeSocket}}
		opts := Options{Channels: map[channel.ChannelID]channel.Channel{"test-ch": ch}}
		sessions := map[channel.ChannelID]string{"test-ch": "old-sess"}

		notifyFreshSessionIfTimedOut(context.Background(), opts, sessions, "test-ch", "live-sess")

		require.Empty(t, ch.sends)
		require.Equal(t, "old-sess", sessions["test-ch"], "session should not be cleared when resolver kept it alive")
	})
}
