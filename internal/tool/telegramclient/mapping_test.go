package telegramclient

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/channel/telegramchannel"
	"tclaw/internal/libraries/store"
)

func TestChannelBotUsernames(t *testing.T) {
	t.Run("maps provisioned bots and skips channels whose teardown state won't parse", func(t *testing.T) {
		ctx := context.Background()

		runtimeStore, err := store.NewFS(t.TempDir())
		require.NoError(t, err)
		runtimeState := channel.NewRuntimeStateStore(runtimeStore)

		// A provisioned telegram channel records its bot username.
		require.NoError(t, runtimeState.Update(ctx, "email", func(rs *channel.RuntimeState) {
			rs.TeardownState = telegramchannel.NewTeardownState("tclaw_ab12cd34_bot")
		}))
		// A statically-configured channel left a telegram teardown state with no
		// data — parsing it fails, and it must be skipped rather than abort the
		// whole listing.
		require.NoError(t, runtimeState.Update(ctx, "admin", func(rs *channel.RuntimeState) {
			rs.TeardownState = channel.TeardownState{Type: channel.PlatformTelegram}
		}))

		registry := channel.NewRegistry([]channel.RegistryEntry{
			{Info: channel.Info{Name: "email", Type: channel.TypeTelegram}},
			{Info: channel.Info{Name: "admin", Type: channel.TypeTelegram}},
			{Info: channel.Info{Name: "scratch", Type: channel.TypeSocket}},
		})

		s := &handlerState{deps: Deps{ChannelRegistry: registry, RuntimeState: runtimeState}}

		got, err := s.channelBotUsernames(ctx)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"tclaw_ab12cd34_bot": "email"}, got)
	})

	t.Run("returns an empty map when the registry and runtime state are absent", func(t *testing.T) {
		s := &handlerState{deps: Deps{}}

		got, err := s.channelBotUsernames(context.Background())
		require.NoError(t, err)
		require.Empty(t, got)
	})
}
