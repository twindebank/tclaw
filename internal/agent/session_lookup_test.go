package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
)

func TestLookupSession(t *testing.T) {
	t.Run("falls back to sessions map when no resolver", func(t *testing.T) {
		sessions := map[channel.ChannelID]string{"ch": "sess-1"}
		got, timedOut := lookupSession(Options{}, sessions, "ch")
		require.Equal(t, "sess-1", got)
		require.False(t, timedOut)
	})

	t.Run("resolver takes precedence over sessions map", func(t *testing.T) {
		sessions := map[channel.ChannelID]string{"ch": "stale-sess"}
		opts := Options{
			SessionResolver: func(chID channel.ChannelID) (string, bool) {
				require.Equal(t, channel.ChannelID("ch"), chID)
				return "live-sess", false
			},
		}
		got, timedOut := lookupSession(opts, sessions, "ch")
		require.Equal(t, "live-sess", got)
		require.False(t, timedOut)
	})

	t.Run("resolver returning empty forces a fresh session", func(t *testing.T) {
		// This is the timeout case: the resolver decides the persisted
		// session is too old to reuse, so the agent should start fresh
		// even though it has a session ID cached in its local map.
		sessions := map[channel.ChannelID]string{"ch": "old-sess"}
		opts := Options{
			SessionResolver: func(chID channel.ChannelID) (string, bool) { return "", true },
		}
		got, timedOut := lookupSession(opts, sessions, "ch")
		require.Equal(t, "", got)
		require.True(t, timedOut)
	})
}

func TestNotifyFreshSessionIfTimedOut(t *testing.T) {
	t.Run("sends notice when prior session is dropped by resolver", func(t *testing.T) {
		ch := &mockChannel{info: channel.Info{ID: "test-ch", Name: "test", Type: channel.TypeSocket}}
		opts := Options{Channels: map[channel.ChannelID]channel.Channel{"test-ch": ch}}
		sessions := map[channel.ChannelID]string{"test-ch": "old-sess"}

		notifyFreshSessionIfTimedOut(context.Background(), opts, sessions, "test-ch", "", false)

		require.Equal(t, []string{freshSessionNotice}, ch.sends)
		_, stillCached := sessions["test-ch"]
		require.False(t, stillCached, "local sessions entry should be cleared so retries don't resend")
	})

	t.Run("silent when no prior session existed", func(t *testing.T) {
		ch := &mockChannel{info: channel.Info{ID: "test-ch", Name: "test", Type: channel.TypeSocket}}
		opts := Options{Channels: map[channel.ChannelID]channel.Channel{"test-ch": ch}}
		sessions := map[channel.ChannelID]string{}

		notifyFreshSessionIfTimedOut(context.Background(), opts, sessions, "test-ch", "", false)

		require.Empty(t, ch.sends)
	})

	t.Run("silent when resolver returned a session", func(t *testing.T) {
		ch := &mockChannel{info: channel.Info{ID: "test-ch", Name: "test", Type: channel.TypeSocket}}
		opts := Options{Channels: map[channel.ChannelID]channel.Channel{"test-ch": ch}}
		sessions := map[channel.ChannelID]string{"test-ch": "old-sess"}

		notifyFreshSessionIfTimedOut(context.Background(), opts, sessions, "test-ch", "live-sess", false)

		require.Empty(t, ch.sends)
		require.Equal(t, "old-sess", sessions["test-ch"], "session should not be cleared when resolver kept it alive")
	})

	t.Run("fires after a router-restart wipes the local sessions cache", func(t *testing.T) {
		// The router rebuilds its in-memory sessions map on every iteration
		// (channel change, reset, idle timeout) by calling
		// sessionStore.CurrentWithin(timeout). A session that's already past
		// its TTL at that moment is filtered out — so the next incoming
		// message sees an empty `sessions[chID]` even though a session did
		// exist before. The notice must still fire: the source of truth for
		// "did a session just time out" is the store, not the in-memory map.
		ctx := context.Background()
		fs, err := store.NewFS(t.TempDir())
		require.NoError(t, err)
		sessionStore := channel.NewSessionStore(fs)

		chID := channel.ChannelID("test-email")
		timeout := 10 * time.Minute

		stale := time.Now().Add(-11 * time.Minute)
		raw, err := json.Marshal([]channel.SessionRecord{{
			SessionID:  "old-sess",
			StartedAt:  stale,
			LastUsedAt: stale,
		}})
		require.NoError(t, err)
		require.NoError(t, fs.Set(ctx, channel.SessionKey(chID), raw))

		// Mimic router.go startup: sessions map is rebuilt with TTL filtering.
		sessions := map[channel.ChannelID]string{}
		sid, err := sessionStore.CurrentWithin(ctx, channel.SessionKey(chID), timeout)
		require.NoError(t, err)
		if sid != "" {
			sessions[chID] = sid
		}
		require.Empty(t, sessions, "stale session must be filtered out on router restart")

		// Resolver shares the same store.
		resolver := func(chID channel.ChannelID) (string, bool) {
			sid, timedOut, err := sessionStore.CurrentWithinDetailed(ctx, channel.SessionKey(chID), timeout)
			if err != nil {
				return "", false
			}
			return sid, timedOut
		}

		ch := &mockChannel{info: channel.Info{ID: chID, Name: "email", Type: channel.TypeTelegram}}
		opts := Options{
			Channels:        map[channel.ChannelID]channel.Channel{chID: ch},
			SessionResolver: resolver,
		}

		resolved, timedOut := lookupSession(opts, sessions, chID)
		require.Equal(t, "", resolved, "stale session resolves to empty")
		require.True(t, timedOut, "resolver must report timedOut for a stale stored record")

		notifyFreshSessionIfTimedOut(ctx, opts, sessions, chID, resolved, timedOut)

		require.Equal(t, []string{freshSessionNotice}, ch.sends,
			"notice must fire for a stored-but-stale session, not just an in-memory cache hit")
	})
}
