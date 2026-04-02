package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/channel/telegramchannel"
	"tclaw/internal/config"
	"tclaw/internal/libraries/store"
	"tclaw/internal/queue"
)

func TestCleanupOnce(t *testing.T) {
	t.Run("skips unknown channel", func(t *testing.T) {
		h := setupEphemeralTest(t, config.Channel{
			Name:      "new-ephemeral",
			Type:      channel.TypeTelegram,
			Ephemeral: true,
		})

		// No activity recorded — tracker returns (false, false).
		_, known := h.tracker.IsBusy("new-ephemeral")
		require.False(t, known)

		cleanupOnce(context.Background(), testUserID, h.configWriter, h.runtimeState,
			h.tracker, h.secretStore, h.provisioners, h.onChannelChange,
			h.lastLoggedError, h.messageQueue, h.channelsFunc)

		channels, err := h.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)
		require.Len(t, channels, 1, "unknown channel should not be cleaned up")
		require.False(t, h.changeCalled)
	})

	t.Run("survives restart with persisted activity", func(t *testing.T) {
		// Simulate: channel was active, then process restarts.
		// The persisted lastMessageAt should prevent cleanup.
		h := setupEphemeralTest(t, config.Channel{
			Name:                 "restarted-ephemeral",
			Type:                 channel.TypeTelegram,
			Ephemeral:            true,
			EphemeralIdleTimeout: "24h",
		})

		// Persist a recent activity timestamp in runtime state (as if the
		// previous process wrote it before dying).
		require.NoError(t, h.runtimeState.Update(context.Background(), "restarted-ephemeral", func(s *channel.RuntimeState) {
			s.LastMessageAt = time.Now().Add(-1 * time.Hour)
			s.LastMessageSource = channel.SourceUser
		}))

		// Create a new tracker that loads from the persisted state (simulating restart).
		h.tracker = channel.NewPersistedActivityTracker(
			context.Background(), h.runtimeState, []string{"restarted-ephemeral"},
		)

		cleanupOnce(context.Background(), testUserID, h.configWriter, h.runtimeState,
			h.tracker, h.secretStore, h.provisioners, h.onChannelChange,
			h.lastLoggedError, h.messageQueue, h.channelsFunc)

		channels, err := h.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)
		require.Len(t, channels, 1, "channel with recent persisted activity should survive restart")
		require.False(t, h.changeCalled)
	})

	t.Run("skips busy channel", func(t *testing.T) {
		h := setupEphemeralTest(t, config.Channel{
			Name:      "busy-ephemeral",
			Type:      channel.TypeTelegram,
			Ephemeral: true,
		})

		// Record activity and keep it busy.
		h.tracker.MessageReceived("busy-ephemeral")
		h.tracker.TurnStarted("busy-ephemeral")

		cleanupOnce(context.Background(), testUserID, h.configWriter, h.runtimeState,
			h.tracker, h.secretStore, h.provisioners, h.onChannelChange,
			h.lastLoggedError, h.messageQueue, h.channelsFunc)

		channels, err := h.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)
		require.Len(t, channels, 1, "busy ephemeral channel should not be cleaned up")
		require.False(t, h.changeCalled)
	})

	t.Run("cleans up idle channel with activity", func(t *testing.T) {
		h := setupEphemeralTest(t, config.Channel{
			Name:                 "idle-ephemeral",
			Type:                 channel.TypeSocket,
			Ephemeral:            true,
			EphemeralIdleTimeout: "1ms",
		})

		// Record activity in the past so the channel is considered idle.
		h.tracker.MessageReceived("idle-ephemeral")
		h.tracker.TurnStarted("idle-ephemeral")
		h.tracker.TurnEnded("idle-ephemeral")
		// Backdate so the 1ms timeout is expired.
		h.tracker.ForceLastMessageAt("idle-ephemeral", time.Now().Add(-time.Second))

		cleanupOnce(context.Background(), testUserID, h.configWriter, h.runtimeState,
			h.tracker, h.secretStore, h.provisioners, h.onChannelChange,
			h.lastLoggedError, h.messageQueue, h.channelsFunc)

		channels, err := h.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)
		require.Empty(t, channels, "idle ephemeral channel should be cleaned up")
		require.True(t, h.changeCalled)
	})

	t.Run("tears down platform resources before removing", func(t *testing.T) {
		h := setupEphemeralTest(t, config.Channel{
			Name:                 "teardown-ephemeral",
			Type:                 channel.TypeTelegram,
			Ephemeral:            true,
			EphemeralIdleTimeout: "1ms",
		})

		// Set up teardown state.
		require.NoError(t, h.runtimeState.Update(context.Background(), "teardown-ephemeral", func(s *channel.RuntimeState) {
			s.TeardownState = telegramchannel.NewTeardownState("test_bot")
		}))

		// Record activity and backdate.
		h.tracker.MessageReceived("teardown-ephemeral")
		h.tracker.ForceLastMessageAt("teardown-ephemeral", time.Now().Add(-time.Second))

		prov := &mockEphemeralProvisioner{}
		h.provisioners = func(ct channel.ChannelType) channel.EphemeralProvisioner {
			if ct == channel.TypeTelegram {
				return prov
			}
			return nil
		}

		cleanupOnce(context.Background(), testUserID, h.configWriter, h.runtimeState,
			h.tracker, h.secretStore, h.provisioners, h.onChannelChange,
			h.lastLoggedError, h.messageQueue, h.channelsFunc)

		require.True(t, prov.teardownCalled, "platform teardown should be called")
		channels, err := h.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)
		require.Empty(t, channels)
	})

	t.Run("skips non-ephemeral channels", func(t *testing.T) {
		h := setupEphemeralTest(t, config.Channel{
			Name:      "permanent",
			Type:      channel.TypeSocket,
			Ephemeral: false,
		})

		// Even with old activity, non-ephemeral channels are never cleaned up.
		h.tracker.MessageReceived("permanent")
		h.tracker.ForceLastMessageAt("permanent", time.Now().Add(-48*time.Hour))

		cleanupOnce(context.Background(), testUserID, h.configWriter, h.runtimeState,
			h.tracker, h.secretStore, h.provisioners, h.onChannelChange,
			h.lastLoggedError, h.messageQueue, h.channelsFunc)

		channels, err := h.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)
		require.Len(t, channels, 1)
	})
}

// --- helpers ---

type ephemeralTestHarness struct {
	configWriter    *config.Writer
	runtimeState    *channel.RuntimeStateStore
	tracker         *channel.ActivityTracker
	secretStore     *memDoneSecretStore
	provisioners    channel.ProvisionerLookup
	onChannelChange func()
	changeCalled    bool
	lastLoggedError map[string]string
	messageQueue    *queue.Queue
	channelsFunc    func() map[channel.ChannelID]channel.Channel
}

func setupEphemeralTest(t *testing.T, ch config.Channel) *ephemeralTestHarness {
	t.Helper()

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "tclaw.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`local:
  users:
    - id: testuser
      channels: []
`), 0o644))

	cw := config.NewWriter(configPath, config.EnvLocal)
	require.NoError(t, cw.AddChannel(testUserID, ch))

	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	activity := channel.NewActivityTracker()
	ss := newMemDoneSecretStore()

	q := queue.New(queue.QueueParams{
		Store:    s,
		Activity: activity,
		Channels: func() map[channel.ChannelID]channel.Channel { return nil },
	})

	h := &ephemeralTestHarness{
		configWriter:    cw,
		runtimeState:    channel.NewRuntimeStateStore(s),
		tracker:         activity,
		secretStore:     ss,
		provisioners:    func(channel.ChannelType) channel.EphemeralProvisioner { return nil },
		lastLoggedError: make(map[string]string),
		messageQueue:    q,
		channelsFunc:    func() map[channel.ChannelID]channel.Channel { return nil },
	}
	h.onChannelChange = func() { h.changeCalled = true }
	return h
}

type mockEphemeralProvisioner struct {
	teardownCalled bool
	teardownErr    error
}

func (m *mockEphemeralProvisioner) IsReady(_ context.Context, _ string) bool { return true }
func (m *mockEphemeralProvisioner) CanAutoProvision() bool                   { return false }
func (m *mockEphemeralProvisioner) ValidateCreate(_ string) error            { return nil }
func (m *mockEphemeralProvisioner) Provision(_ context.Context, _ channel.ProvisionParams) (*channel.ProvisionResult, error) {
	return nil, nil
}
func (m *mockEphemeralProvisioner) Teardown(_ context.Context, _ channel.TeardownState) error {
	m.teardownCalled = true
	return m.teardownErr
}
func (m *mockEphemeralProvisioner) SendTeardownPrompt(_ context.Context, _ string, _ channel.PlatformState) error {
	return nil
}
func (m *mockEphemeralProvisioner) SendClosingMessage(_ context.Context, _ string, _ channel.PlatformState) error {
	return nil
}
func (m *mockEphemeralProvisioner) Notify(_ context.Context, _ string, _ string) error {
	return nil
}
func (m *mockEphemeralProvisioner) PlatformResponseInfo(_ channel.TeardownState) map[string]any {
	return nil
}
