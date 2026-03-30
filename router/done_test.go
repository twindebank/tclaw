package router

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/channel/telegramchannel"
	"tclaw/config"
	"tclaw/libraries/store"
	"tclaw/user"
)

const testUserID user.ID = "testuser"

func TestInterceptPendingDone(t *testing.T) {
	t.Run("passes through when channel has no pending_done", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)
		prov := &mockDoneProvisioner{}
		var changeCalled bool

		consumed := interceptPendingDone(
			context.Background(),
			doneTaggedMsg("mychan-id", "yes"),
			doneChannelsFunc("mychan-id", "mychan", channel.TypeSocket),
			rs, cw, testUserID, ss,
			map[channel.ChannelType]channel.EphemeralProvisioner{channel.TypeSocket: prov},
			func() { changeCalled = true },
		)

		require.False(t, consumed)
		require.False(t, prov.teardownCalled)
		require.False(t, changeCalled)
	})

	t.Run("tears down on yes", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)

		// Set pending done + teardown state in runtime state.
		require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
			s.PendingDone = true
			s.TeardownState = telegramchannel.NewTeardownState("tclaw_test_bot")
		}))
		require.NoError(t, ss.Set(context.Background(), channel.ChannelSecretKey("ephemeral"), "fake-token"))

		// Add channel to config so RemoveChannel works.
		require.NoError(t, cw.AddChannel(testUserID, config.Channel{
			Type: channel.TypeTelegram, Name: "ephemeral", Description: "test",
		}))

		prov := &mockDoneProvisioner{}
		var changeCalled bool

		consumed := interceptPendingDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "yes"),
			doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeTelegram),
			rs, cw, testUserID, ss,
			map[channel.ChannelType]channel.EphemeralProvisioner{channel.TypeTelegram: prov},
			func() { changeCalled = true },
		)

		require.True(t, consumed)
		require.True(t, prov.teardownCalled)
		require.True(t, changeCalled)

		// Secret should be gone.
		token, err := ss.Get(context.Background(), channel.ChannelSecretKey("ephemeral"))
		require.NoError(t, err)
		require.Empty(t, token)
	})

	t.Run("sends closing message before teardown when platform state present", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)

		require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
			s.PendingDone = true
			s.PlatformState = telegramchannel.NewPlatformState(12345)
			s.TeardownState = telegramchannel.NewTeardownState("tclaw_test_bot")
		}))
		require.NoError(t, ss.Set(context.Background(), channel.ChannelSecretKey("ephemeral"), "fake-token"))
		require.NoError(t, cw.AddChannel(testUserID, config.Channel{
			Type: channel.TypeTelegram, Name: "ephemeral", Description: "test",
		}))

		prov := &mockDoneProvisioner{}

		consumed := interceptPendingDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "yes"),
			doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeTelegram),
			rs, cw, testUserID, ss,
			map[channel.ChannelType]channel.EphemeralProvisioner{channel.TypeTelegram: prov},
			nil,
		)

		require.True(t, consumed)
		require.True(t, prov.closingMessageCalled, "closing message should be sent before teardown")
		require.True(t, prov.teardownCalled)
	})

	t.Run("clears flag and passes through on non-yes reply", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)

		require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
			s.PendingDone = true
		}))

		prov := &mockDoneProvisioner{}
		var changeCalled bool

		consumed := interceptPendingDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "no"),
			doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeSocket),
			rs, cw, testUserID, ss,
			map[channel.ChannelType]channel.EphemeralProvisioner{channel.TypeSocket: prov},
			func() { changeCalled = true },
		)

		require.False(t, consumed)
		require.False(t, prov.teardownCalled)
		require.False(t, changeCalled)

		// PendingDone should be cleared.
		state, err := rs.Get(context.Background(), "ephemeral")
		require.NoError(t, err)
		require.False(t, state.PendingDone)
	})

	t.Run("does not delete config if teardown fails", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)

		require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
			s.PendingDone = true
			s.TeardownState = telegramchannel.NewTeardownState("tclaw_test_bot")
		}))
		require.NoError(t, cw.AddChannel(testUserID, config.Channel{
			Type: channel.TypeTelegram, Name: "ephemeral", Description: "test",
		}))

		prov := &mockDoneProvisioner{teardownErr: fmt.Errorf("BotFather unreachable")}
		var changeCalled bool

		consumed := interceptPendingDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "yes"),
			doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeTelegram),
			rs, cw, testUserID, ss,
			map[channel.ChannelType]channel.EphemeralProvisioner{channel.TypeTelegram: prov},
			func() { changeCalled = true },
		)

		// Message consumed but channel survives.
		require.True(t, consumed)
		require.False(t, changeCalled)

		// Channel should still be in config.
		channels, err := cw.ReadChannels(testUserID)
		require.NoError(t, err)
		require.Len(t, channels, 1)
	})
}

// --- helpers ---

func setupDoneTest(t *testing.T) (*channel.RuntimeStateStore, *memDoneSecretStore, *config.Writer) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	// Create a minimal config file for the writer.
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "tclaw.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`local:
  users:
    - id: testuser
      channels: []
`), 0o644))

	cw := config.NewWriter(configPath, config.EnvLocal)
	rs := channel.NewRuntimeStateStore(s)
	return rs, newMemDoneSecretStore(), cw
}

func doneChannelsFunc(id, name string, chType channel.ChannelType) func() map[channel.ChannelID]channel.Channel {
	ch := &stubDoneChannel{info: channel.Info{ID: channel.ChannelID(id), Name: name, Type: chType}}
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

func (m *mockDoneProvisioner) IsReady(_ context.Context, _ string) bool { return true }
func (m *mockDoneProvisioner) CanAutoProvision() bool                   { return false }
func (m *mockDoneProvisioner) ValidateCreate(_ string) error {
	return nil
}
func (m *mockDoneProvisioner) Provision(_ context.Context, _ channel.ProvisionParams) (*channel.ProvisionResult, error) {
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
func (m *mockDoneProvisioner) Notify(_ context.Context, _ string, _ string) error {
	return nil
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
