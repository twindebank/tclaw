package telegramchannel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/channel/channelpkg"
	"tclaw/internal/channel/telegramchannel"
	"tclaw/internal/config"
	"tclaw/internal/libraries/store"
)

func TestPackage_Type(t *testing.T) {
	pkg := telegramchannel.NewPackage(nil)
	require.Equal(t, channel.TypeTelegram, pkg.Type())
}

func TestPackage_Build(t *testing.T) {
	t.Run("fails without token", func(t *testing.T) {
		pkg := telegramchannel.NewPackage(nil)
		stateStore, err := store.NewFS(t.TempDir())
		require.NoError(t, err)
		_, err = pkg.Build(context.Background(), channelpkg.BuildParams{
			ChannelCfg:  config.Channel{Name: "notoken", Description: "test"},
			Env:         config.EnvLocal,
			BaseDir:     t.TempDir(),
			UserID:      "testuser",
			SecretStore: &memorySecretStore{data: map[string]string{}},
			StateStore:  stateStore,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "no bot token")
	})

	t.Run("succeeds with inline token", func(t *testing.T) {
		pkg := telegramchannel.NewPackage(nil)
		stateStore, err := store.NewFS(t.TempDir())
		require.NoError(t, err)
		ch, err := pkg.Build(context.Background(), channelpkg.BuildParams{
			ChannelCfg: config.Channel{
				Name:        "mybot",
				Description: "test bot",
				Telegram:    &config.TelegramChannelConfig{Token: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"},
			},
			Env:        config.EnvLocal,
			BaseDir:    t.TempDir(),
			UserID:     "testuser",
			StateStore: stateStore,
		})
		require.NoError(t, err)
		require.Equal(t, channel.TypeTelegram, ch.Info().Type)
		require.Equal(t, "mybot", ch.Info().Name)
	})
}

func TestPackage_Provisioner(t *testing.T) {
	t.Run("nil when no provisioner", func(t *testing.T) {
		pkg := telegramchannel.NewPackage(nil)
		require.Nil(t, pkg.Provisioner())
	})

	t.Run("returns provisioner when set", func(t *testing.T) {
		prov := &stubProvisioner{}
		pkg := telegramchannel.NewPackage(prov)
		require.Same(t, prov, pkg.Provisioner())
	})
}

// --- helpers ---

type memorySecretStore struct {
	data map[string]string
}

func (m *memorySecretStore) Get(_ context.Context, key string) (string, error) {
	return m.data[key], nil
}

func (m *memorySecretStore) Set(_ context.Context, key, value string) error {
	m.data[key] = value
	return nil
}

func (m *memorySecretStore) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

type stubProvisioner struct{}

func (s *stubProvisioner) IsReady(_ context.Context, _ string) bool { return false }
func (s *stubProvisioner) CanAutoProvision() bool                   { return false }
func (s *stubProvisioner) ValidateCreate(_ string) error            { return nil }
func (s *stubProvisioner) Provision(_ context.Context, _ channel.ProvisionParams) (*channel.ProvisionResult, error) {
	return nil, nil
}
func (s *stubProvisioner) Teardown(_ context.Context, _ channel.TeardownState) error { return nil }
func (s *stubProvisioner) SendTeardownPrompt(_ context.Context, _ string, _ channel.PlatformState) error {
	return nil
}
func (s *stubProvisioner) SendClosingMessage(_ context.Context, _ string, _ channel.PlatformState) error {
	return nil
}
func (s *stubProvisioner) Notify(_ context.Context, _ string, _ string) error { return nil }
func (s *stubProvisioner) PlatformResponseInfo(_ channel.TeardownState) map[string]any {
	return nil
}
