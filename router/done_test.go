package router

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/libraries/store"
)

func TestInterceptPendingDone(t *testing.T) {
	t.Run("passes through when channel has no pending_done", func(t *testing.T) {
		ds, ss := setupDoneTest(t)
		require.NoError(t, ds.Add(context.Background(), channel.DynamicChannelConfig{
			Name: "mychan",
			Type: channel.TypeSocket,
		}))

		prov := &mockDoneProvisioner{}
		var changeCalled bool

		consumed := interceptPendingDone(
			context.Background(),
			doneTaggedMsg("mychan-id", "yes"),
			doneChannelsFunc("mychan-id", "mychan"),
			ds, ss,
			map[channel.ChannelType]channel.EphemeralProvisioner{channel.TypeSocket: prov},
			func() { changeCalled = true },
		)

		require.False(t, consumed)
		require.False(t, prov.teardownCalled)
		require.False(t, changeCalled)
	})

	t.Run("tears down on yes", func(t *testing.T) {
		ds, ss := setupDoneTest(t)
		require.NoError(t, ds.Add(context.Background(), channel.DynamicChannelConfig{
			Name:        "ephemeral",
			Type:        channel.TypeTelegram,
			PendingDone: true,
			TeardownState: channel.TelegramTeardownState{
				BotUsername: "tclaw_test_bot",
			},
		}))
		require.NoError(t, ss.Set(context.Background(), channel.ChannelSecretKey("ephemeral"), "fake-token"))

		prov := &mockDoneProvisioner{}
		var changeCalled bool

		consumed := interceptPendingDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "yes"),
			doneChannelsFunc("ephemeral-id", "ephemeral"),
			ds, ss,
			map[channel.ChannelType]channel.EphemeralProvisioner{channel.TypeTelegram: prov},
			func() { changeCalled = true },
		)

		require.True(t, consumed)
		require.True(t, prov.teardownCalled)
		require.True(t, changeCalled)

		// Channel config should be gone.
		cfg, err := ds.Get(context.Background(), "ephemeral")
		require.NoError(t, err)
		require.Nil(t, cfg)

		// Secret should be gone.
		token, err := ss.Get(context.Background(), channel.ChannelSecretKey("ephemeral"))
		require.NoError(t, err)
		require.Empty(t, token)
	})

	t.Run("sends closing message before teardown when platform state present", func(t *testing.T) {
		ds, ss := setupDoneTest(t)
		require.NoError(t, ds.Add(context.Background(), channel.DynamicChannelConfig{
			Name:          "ephemeral",
			Type:          channel.TypeTelegram,
			PendingDone:   true,
			PlatformState: channel.TelegramPlatformState{ChatID: 12345},
			TeardownState: channel.TelegramTeardownState{BotUsername: "tclaw_test_bot"},
		}))
		require.NoError(t, ss.Set(context.Background(), channel.ChannelSecretKey("ephemeral"), "fake-token"))

		prov := &mockDoneProvisioner{}

		consumed := interceptPendingDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "yes"),
			doneChannelsFunc("ephemeral-id", "ephemeral"),
			ds, ss,
			map[channel.ChannelType]channel.EphemeralProvisioner{channel.TypeTelegram: prov},
			nil,
		)

		require.True(t, consumed)
		require.True(t, prov.closingMessageCalled, "closing message should be sent before teardown")
		require.True(t, prov.teardownCalled)
	})

	t.Run("accepts y as confirmation", func(t *testing.T) {
		ds, ss := setupDoneTest(t)
		require.NoError(t, ds.Add(context.Background(), channel.DynamicChannelConfig{
			Name:        "ephemeral",
			Type:        channel.TypeSocket,
			PendingDone: true,
		}))

		consumed := interceptPendingDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "y"),
			doneChannelsFunc("ephemeral-id", "ephemeral"),
			ds, ss,
			map[channel.ChannelType]channel.EphemeralProvisioner{},
			nil,
		)

		require.True(t, consumed)

		cfg, err := ds.Get(context.Background(), "ephemeral")
		require.NoError(t, err)
		require.Nil(t, cfg)
	})

	t.Run("clears flag and passes through on non-yes reply", func(t *testing.T) {
		ds, ss := setupDoneTest(t)
		require.NoError(t, ds.Add(context.Background(), channel.DynamicChannelConfig{
			Name:        "ephemeral",
			Type:        channel.TypeSocket,
			PendingDone: true,
		}))

		prov := &mockDoneProvisioner{}
		var changeCalled bool

		consumed := interceptPendingDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "no"),
			doneChannelsFunc("ephemeral-id", "ephemeral"),
			ds, ss,
			map[channel.ChannelType]channel.EphemeralProvisioner{channel.TypeSocket: prov},
			func() { changeCalled = true },
		)

		// Message should reach the agent.
		require.False(t, consumed)
		require.False(t, prov.teardownCalled)
		require.False(t, changeCalled)

		// PendingDone should be cleared so next message is handled normally.
		cfg, err := ds.Get(context.Background(), "ephemeral")
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.False(t, cfg.PendingDone)
	})

	t.Run("passes through for unknown channel ID", func(t *testing.T) {
		ds, ss := setupDoneTest(t)

		consumed := interceptPendingDone(
			context.Background(),
			doneTaggedMsg("unknown-id", "yes"),
			doneChannelsFunc("other-id", "other"),
			ds, ss,
			map[channel.ChannelType]channel.EphemeralProvisioner{},
			nil,
		)

		require.False(t, consumed)
	})

	t.Run("does not delete config if teardown fails", func(t *testing.T) {
		ds, ss := setupDoneTest(t)
		require.NoError(t, ds.Add(context.Background(), channel.DynamicChannelConfig{
			Name:        "ephemeral",
			Type:        channel.TypeTelegram,
			PendingDone: true,
			TeardownState: channel.TelegramTeardownState{
				BotUsername: "tclaw_test_bot",
			},
		}))

		prov := &mockDoneProvisioner{teardownErr: fmt.Errorf("BotFather unreachable")}
		var changeCalled bool

		consumed := interceptPendingDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "yes"),
			doneChannelsFunc("ephemeral-id", "ephemeral"),
			ds, ss,
			map[channel.ChannelType]channel.EphemeralProvisioner{channel.TypeTelegram: prov},
			func() { changeCalled = true },
		)

		// Message is consumed (not forwarded to agent), but channel survives.
		require.True(t, consumed)
		require.False(t, changeCalled)

		cfg, err := ds.Get(context.Background(), "ephemeral")
		require.NoError(t, err)
		require.NotNil(t, cfg, "channel config must survive a failed teardown")
	})
}

// --- helpers ---

func setupDoneTest(t *testing.T) (*channel.DynamicStore, *memDoneSecretStore) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return channel.NewDynamicStore(s), newMemDoneSecretStore()
}

func doneChannelsFunc(id, name string) func() map[channel.ChannelID]channel.Channel {
	ch := &stubDoneChannel{info: channel.Info{ID: channel.ChannelID(id), Name: name}}
	m := map[channel.ChannelID]channel.Channel{channel.ChannelID(id): ch}
	return func() map[channel.ChannelID]channel.Channel { return m }
}

func doneTaggedMsg(channelID, text string) channel.TaggedMessage {
	return channel.TaggedMessage{
		ChannelID: channel.ChannelID(channelID),
		Text:      text,
	}
}

type stubDoneChannel struct {
	info channel.Info
}

func (s *stubDoneChannel) Info() channel.Info                       { return s.info }
func (s *stubDoneChannel) Messages(_ context.Context) <-chan string { return nil }
func (s *stubDoneChannel) Send(_ context.Context, _ string) (channel.MessageID, error) {
	return "", nil
}
func (s *stubDoneChannel) Edit(_ context.Context, _ channel.MessageID, _ string) error { return nil }
func (s *stubDoneChannel) Done(_ context.Context) error                                { return nil }
func (s *stubDoneChannel) SplitStatusMessages() bool                                   { return false }
func (s *stubDoneChannel) Markup() channel.Markup                                      { return channel.MarkupMarkdown }
func (s *stubDoneChannel) StatusWrap() channel.StatusWrap                              { return channel.StatusWrap{} }

type mockDoneProvisioner struct {
	teardownCalled       bool
	teardownErr          error
	closingMessageCalled bool
}

func (m *mockDoneProvisioner) ValidateCreate(_ []int64, _ string) error {
	return nil
}

func (m *mockDoneProvisioner) Provision(_ context.Context, _, _ string) (*channel.ProvisionResult, error) {
	return nil, nil
}

func (m *mockDoneProvisioner) Teardown(_ context.Context, _ channel.TeardownState) error {
	m.teardownCalled = true
	return m.teardownErr
}

func (m *mockDoneProvisioner) SendTeardownPrompt(_ context.Context, _ string, _ channel.PlatformState) error {
	return nil
}

func (m *mockDoneProvisioner) SendClosingMessage(_ context.Context, _ string, _ channel.PlatformState) error {
	m.closingMessageCalled = true
	return nil
}

func (m *mockDoneProvisioner) Notify(_ context.Context, _ string, _ []int64, _ string) (int, error) {
	return 0, nil
}

func (m *mockDoneProvisioner) PlatformResponseInfo(_ channel.TeardownState) map[string]any {
	return nil
}

type memDoneSecretStore struct {
	data map[string]string
}

func newMemDoneSecretStore() *memDoneSecretStore {
	return &memDoneSecretStore{data: make(map[string]string)}
}

func (m *memDoneSecretStore) Get(_ context.Context, key string) (string, error) {
	return m.data[key], nil
}

func (m *memDoneSecretStore) Set(_ context.Context, key, value string) error {
	m.data[key] = value
	return nil
}

func (m *memDoneSecretStore) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}
