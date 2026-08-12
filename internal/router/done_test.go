package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/channel/telegramchannel"
	"tclaw/internal/config"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/repo"
	"tclaw/internal/user"
)

const testUserID user.ID = "testuser"

func TestInterceptPendingDone(t *testing.T) {
	t.Run("passes through when channel has no pending_done", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)
		prov := &mockDoneProvisioner{}
		var changeCalled bool

		consumed := interceptDone(
			context.Background(),
			doneTaggedMsg("mychan-id", "yes"),
			doneChannelsFunc("mychan-id", "mychan", channel.TypeSocket),
			rs, cw, testUserID, ss,
			provLookup(channel.TypeSocket, prov),
			func() { changeCalled = true },
			"",
		)

		require.False(t, consumed)
		require.False(t, prov.teardownCalled)
		require.False(t, changeCalled)
	})

	t.Run("tears down on yes", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)

		// Set pending done + teardown state in runtime state.
		require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
			s.PendingAction = channel.NewPendingAction(channel.PendingChannelDone, nil)
			s.TeardownState = telegramchannel.NewTeardownState("tclaw_test_bot")
		}))
		require.NoError(t, ss.Set(context.Background(), channel.ChannelSecretKey("ephemeral"), "fake-token"))

		// Add channel to config so RemoveChannel works.
		require.NoError(t, cw.AddChannel(testUserID, config.Channel{
			Type: channel.TypeTelegram, Name: "ephemeral", Description: "test",
		}))

		prov := &mockDoneProvisioner{}
		var changeCalled bool

		consumed := interceptDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "yes"),
			doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeTelegram),
			rs, cw, testUserID, ss,
			provLookup(channel.TypeTelegram, prov),
			func() { changeCalled = true },
			"",
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
			s.PendingAction = channel.NewPendingAction(channel.PendingChannelDone, nil)
			s.PlatformState = telegramchannel.NewPlatformState(12345)
			s.TeardownState = telegramchannel.NewTeardownState("tclaw_test_bot")
		}))
		require.NoError(t, ss.Set(context.Background(), channel.ChannelSecretKey("ephemeral"), "fake-token"))
		require.NoError(t, cw.AddChannel(testUserID, config.Channel{
			Type: channel.TypeTelegram, Name: "ephemeral", Description: "test",
		}))

		prov := &mockDoneProvisioner{}

		consumed := interceptDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "yes"),
			doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeTelegram),
			rs, cw, testUserID, ss,
			provLookup(channel.TypeTelegram, prov),
			nil,
			"",
		)

		require.True(t, consumed)
		require.True(t, prov.closingMessageCalled, "closing message should be sent before teardown")
		require.True(t, prov.teardownCalled)
	})

	t.Run("clears flag and passes through on non-yes reply", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)

		require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
			s.PendingAction = channel.NewPendingAction(channel.PendingChannelDone, nil)
		}))

		prov := &mockDoneProvisioner{}
		var changeCalled bool

		consumed := interceptDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "no"),
			doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeSocket),
			rs, cw, testUserID, ss,
			provLookup(channel.TypeSocket, prov),
			func() { changeCalled = true },
			"",
		)

		require.False(t, consumed)
		require.False(t, prov.teardownCalled)
		require.False(t, changeCalled)

		// The confirmation should be disarmed.
		state, err := rs.Get(context.Background(), "ephemeral")
		require.NoError(t, err)
		require.Nil(t, state.PendingAction)
	})

	t.Run("accepts y as confirmation", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)

		require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
			s.PendingAction = channel.NewPendingAction(channel.PendingChannelDone, nil)
			s.TeardownState = telegramchannel.NewTeardownState("tclaw_test_bot")
		}))
		require.NoError(t, ss.Set(context.Background(), channel.ChannelSecretKey("ephemeral"), "fake-token"))
		require.NoError(t, cw.AddChannel(testUserID, config.Channel{
			Type: channel.TypeTelegram, Name: "ephemeral", Description: "test",
		}))

		prov := &mockDoneProvisioner{}

		consumed := interceptDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "y"),
			doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeTelegram),
			rs, cw, testUserID, ss,
			provLookup(channel.TypeTelegram, prov),
			nil, "",
		)

		require.True(t, consumed)
		require.True(t, prov.teardownCalled)
	})

	t.Run("accepts YES with whitespace and mixed case", func(t *testing.T) {
		for _, input := range []string{"YES", " Yes ", "  y  ", "Y"} {
			t.Run(input, func(t *testing.T) {
				rs, ss, cw := setupDoneTest(t)

				require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
					s.PendingAction = channel.NewPendingAction(channel.PendingChannelDone, nil)
					s.TeardownState = telegramchannel.NewTeardownState("tclaw_test_bot")
				}))
				require.NoError(t, ss.Set(context.Background(), channel.ChannelSecretKey("ephemeral"), "fake-token"))
				require.NoError(t, cw.AddChannel(testUserID, config.Channel{
					Type: channel.TypeTelegram, Name: "ephemeral", Description: "test",
				}))

				consumed := interceptDone(
					context.Background(),
					doneTaggedMsg("ephemeral-id", input),
					doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeTelegram),
					rs, cw, testUserID, ss,
					provLookup(channel.TypeTelegram, &mockDoneProvisioner{}),
					nil, "",
				)

				require.True(t, consumed, "input %q should be accepted as confirmation", input)
			})
		}
	})

	t.Run("rejects partial matches like yes please", func(t *testing.T) {
		for _, input := range []string{"yes please", "yeah", "yep", "ok", "sure"} {
			t.Run(input, func(t *testing.T) {
				rs, _, cw := setupDoneTest(t)

				require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
					s.PendingAction = channel.NewPendingAction(channel.PendingChannelDone, nil)
				}))

				consumed := interceptDone(
					context.Background(),
					doneTaggedMsg("ephemeral-id", input),
					doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeSocket),
					rs, cw, testUserID, nil,
					provLookup(channel.TypeSocket, &mockDoneProvisioner{}),
					nil, "",
				)

				require.False(t, consumed, "input %q should NOT be accepted as confirmation", input)

				// The confirmation should be disarmed.
				state, err := rs.Get(context.Background(), "ephemeral")
				require.NoError(t, err)
				require.Nil(t, state.PendingAction)
			})
		}
	})

	t.Run("cleans up knowledge dir on teardown", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)
		memoryDir := t.TempDir()

		// Seed a knowledge dir with content.
		knowledgeDir := filepath.Join(memoryDir, "channels", "ephemeral")
		require.NoError(t, os.MkdirAll(knowledgeDir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(knowledgeDir, "CLAUDE.md"), []byte("test"), 0o600))

		require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
			s.PendingAction = channel.NewPendingAction(channel.PendingChannelDone, nil)
			s.TeardownState = telegramchannel.NewTeardownState("tclaw_test_bot")
		}))
		require.NoError(t, ss.Set(context.Background(), channel.ChannelSecretKey("ephemeral"), "fake-token"))
		require.NoError(t, cw.AddChannel(testUserID, config.Channel{
			Type: channel.TypeTelegram, Name: "ephemeral", Description: "test",
		}))

		consumed := interceptDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "yes"),
			doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeTelegram),
			rs, cw, testUserID, ss,
			provLookup(channel.TypeTelegram, &mockDoneProvisioner{}),
			nil, memoryDir,
		)

		require.True(t, consumed)

		// Knowledge dir should be removed.
		_, err := os.Stat(knowledgeDir)
		require.True(t, os.IsNotExist(err), "knowledge dir should be deleted after teardown")
	})

	t.Run("does not delete config if teardown fails", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)

		require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
			s.PendingAction = channel.NewPendingAction(channel.PendingChannelDone, nil)
			s.TeardownState = telegramchannel.NewTeardownState("tclaw_test_bot")
		}))
		require.NoError(t, cw.AddChannel(testUserID, config.Channel{
			Type: channel.TypeTelegram, Name: "ephemeral", Description: "test",
		}))

		prov := &mockDoneProvisioner{teardownErr: fmt.Errorf("BotFather unreachable")}
		var changeCalled bool

		consumed := interceptDone(
			context.Background(),
			doneTaggedMsg("ephemeral-id", "yes"),
			doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeTelegram),
			rs, cw, testUserID, ss,
			provLookup(channel.TypeTelegram, prov),
			func() { changeCalled = true },
			"",
		)

		// Message consumed but channel survives.
		require.True(t, consumed)
		require.False(t, changeCalled)

		// Channel should still be in config.
		channels, err := cw.ReadChannels(testUserID)
		require.NoError(t, err)
		require.Len(t, channels, 1)
	})

	t.Run("ignores a non-user yes and keeps the confirmation armed", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)

		require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
			s.PendingAction = channel.NewPendingAction(channel.PendingChannelDone, nil)
			s.TeardownState = telegramchannel.NewTeardownState("tclaw_test_bot")
		}))
		require.NoError(t, ss.Set(context.Background(), channel.ChannelSecretKey("ephemeral"), "fake-token"))
		require.NoError(t, cw.AddChannel(testUserID, config.Channel{
			Type: channel.TypeTelegram, Name: "ephemeral", Description: "test",
		}))

		prov := &mockDoneProvisioner{}

		// A notification (or any non-user source) that happens to say "yes" must
		// not be able to confirm the teardown — only a real user can.
		consumed := interceptDone(
			context.Background(),
			doneTaggedMsgFrom("ephemeral-id", "yes", channel.SourceNotification),
			doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeTelegram),
			rs, cw, testUserID, ss,
			provLookup(channel.TypeTelegram, prov),
			nil, "",
		)

		require.False(t, consumed, "non-user message must be forwarded, not consumed as confirmation")
		require.False(t, prov.teardownCalled, "non-user yes must not trigger teardown")

		// The confirmation stays armed so the user's real reply still lands.
		state, err := rs.Get(context.Background(), "ephemeral")
		require.NoError(t, err)
		require.NotNil(t, state.PendingAction, "the confirmation must remain armed after a non-user message")
	})

	t.Run("does not let a non-user message cancel a pending teardown", func(t *testing.T) {
		rs, ss, cw := setupDoneTest(t)

		require.NoError(t, rs.Update(context.Background(), "ephemeral", func(s *channel.RuntimeState) {
			s.PendingAction = channel.NewPendingAction(channel.PendingChannelDone, nil)
		}))

		consumed := interceptDone(
			context.Background(),
			doneTaggedMsgFrom("ephemeral-id", "some cross-channel update", channel.SourceChannel),
			doneChannelsFunc("ephemeral-id", "ephemeral", channel.TypeSocket),
			rs, cw, testUserID, ss,
			provLookup(channel.TypeSocket, &mockDoneProvisioner{}),
			nil, "",
		)

		require.False(t, consumed)

		// The flag must NOT be cleared by automated traffic — otherwise a stray
		// notification would silently cancel the pending teardown.
		state, err := rs.Get(context.Background(), "ephemeral")
		require.NoError(t, err)
		require.NotNil(t, state.PendingAction)
	})
}

// --- helpers ---

func provLookup(ct channel.ChannelType, prov channel.EphemeralProvisioner) channel.ProvisionerLookup {
	return func(t channel.ChannelType) channel.EphemeralProvisioner {
		if t == ct {
			return prov
		}
		return nil
	}
}

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

// doneTaggedMsgFrom builds a message tagged with a specific source so the
// user-vs-automated gating in the confirmation intercept can be exercised.
func doneTaggedMsgFrom(channelID, text string, source channel.MessageSource) channel.TaggedMessage {
	return channel.TaggedMessage{
		ChannelID:  channel.ChannelID(channelID),
		Text:       text,
		SourceInfo: &channel.MessageSourceInfo{Source: source},
	}
}

type stubDoneChannel struct {
	info channel.Info
}

func (s *stubDoneChannel) Info() channel.Info                       { return s.info }
func (s *stubDoneChannel) Messages(_ context.Context) <-chan string { return nil }
func (s *stubDoneChannel) Send(_ context.Context, _ string, _ channel.SendOpts) (channel.MessageID, error) {
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

// --- confirmation dispatch helpers ---

// interceptDone adapts the positional call the channel_done tests were written
// against onto the generalised confirmation dispatcher.
func interceptDone(
	ctx context.Context,
	msg channel.TaggedMessage,
	channelsFunc func() map[channel.ChannelID]channel.Channel,
	runtimeState *channel.RuntimeStateStore,
	configWriter *config.Writer,
	userID user.ID,
	secretStore secret.Store,
	provisioners channel.ProvisionerLookup,
	onChannelChange func(),
	memoryDir string,
) bool {
	return interceptPendingConfirmation(ctx, msg, confirmParams{
		ChannelsFunc:    channelsFunc,
		RuntimeState:    runtimeState,
		ConfigWriter:    configWriter,
		UserID:          userID,
		SecretStore:     secretStore,
		Provisioners:    provisioners,
		OnChannelChange: onChannelChange,
		MemoryDir:       memoryDir,
	})
}

func TestInterceptPendingConfirmation_Expiry(t *testing.T) {
	t.Run("an expired confirmation is not acted on", func(t *testing.T) {
		// A "yes" typed into a forgotten thread must not act on an old prompt.
		rs, ss, cw := setupDoneTest(t)
		prov := &mockDoneProvisioner{}

		require.NoError(t, rs.Update(context.Background(), "mychan", func(s *channel.RuntimeState) {
			s.PendingAction = &channel.PendingAction{
				Kind:        channel.PendingChannelDone,
				RequestedAt: time.Now().Add(-2 * time.Hour),
				ExpiresAt:   time.Now().Add(-time.Hour),
			}
		}))

		consumed := interceptDone(
			context.Background(),
			doneTaggedMsg("mychan-id", "yes"),
			doneChannelsFunc("mychan-id", "mychan", channel.TypeSocket),
			rs, cw, testUserID, ss,
			provLookup(channel.TypeSocket, prov),
			func() {}, "",
		)

		require.False(t, consumed, "an expired confirmation must fall through to the agent")
		require.False(t, prov.teardownCalled)

		state, err := rs.Get(context.Background(), "mychan")
		require.NoError(t, err)
		require.Nil(t, state.PendingAction, "the stale confirmation should be disarmed")
	})
}

func TestConfirmRepoGrant(t *testing.T) {
	newGrant := func(t *testing.T, payload RepoGrantPayload) channel.PendingAction {
		t.Helper()
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		return *channel.NewPendingAction(channel.PendingRepoGrant, raw)
	}

	t.Run("applies the tier that was described in the prompt", func(t *testing.T) {
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)
		putRepo(t, repoStore, "ha-config", userDir, nil)

		var notified string
		consumed := confirmRepoGrant(context.Background(), "chan-id", "homeassistant",
			newGrant(t, RepoGrantPayload{
				Repo:       "ha-config",
				Access:     repo.AccessPullRequestsOnly,
				Credential: "homeassistant",
			}),
			confirmParams{
				RepoStore: repoStore,
				Notify:    func(_ context.Context, _ channel.ChannelID, text string) { notified = text },
			})

		require.True(t, consumed)
		granted, err := repoStore.Get(context.Background(), "ha-config")
		require.NoError(t, err)
		require.Equal(t, repo.AccessPullRequestsOnly, granted.Access)
		require.Equal(t, "homeassistant", granted.Credential)
		require.Contains(t, notified, "pull_requests_only")
	})

	t.Run("refuses a repo scoped away from the confirming channel", func(t *testing.T) {
		// Scoping can change between prompt and reply; the grant must not
		// escape the channel that is allowed to see the repo.
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)
		putRepo(t, repoStore, "ha-config", userDir, []string{"homeassistant"})

		var notified string
		consumed := confirmRepoGrant(context.Background(), "chan-id", "email",
			newGrant(t, RepoGrantPayload{Repo: "ha-config", Access: repo.AccessFullWrite}),
			confirmParams{
				RepoStore: repoStore,
				Notify:    func(_ context.Context, _ channel.ChannelID, text string) { notified = text },
			})

		require.True(t, consumed)
		require.Contains(t, notified, "isn't available on this channel")

		unchanged, err := repoStore.Get(context.Background(), "ha-config")
		require.NoError(t, err)
		require.Empty(t, unchanged.Access)
	})

	t.Run("reports a repo that vanished between prompt and reply", func(t *testing.T) {
		userDir := t.TempDir()
		repoStore := newRepoStore(t, userDir)

		var notified string
		consumed := confirmRepoGrant(context.Background(), "chan-id", "homeassistant",
			newGrant(t, RepoGrantPayload{Repo: "gone", Access: repo.AccessFullWrite}),
			confirmParams{
				RepoStore: repoStore,
				Notify:    func(_ context.Context, _ channel.ChannelID, text string) { notified = text },
			})

		require.True(t, consumed)
		require.Contains(t, notified, "no longer tracked")
	})
}
