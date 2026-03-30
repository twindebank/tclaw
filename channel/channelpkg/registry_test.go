package channelpkg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/channel/channelpkg"
)

func TestRegistry_Build(t *testing.T) {
	t.Run("known type returns channel", func(t *testing.T) {
		reg := channelpkg.NewRegistry(&stubPackage{channelType: "test"})

		ch, err := reg.Build(context.Background(), "test", channelpkg.BuildParams{})
		require.NoError(t, err)
		require.Equal(t, channel.ChannelType("test"), ch.Info().Type)
	})

	t.Run("unknown type returns error", func(t *testing.T) {
		reg := channelpkg.NewRegistry(&stubPackage{channelType: "test"})

		_, err := reg.Build(context.Background(), "unknown", channelpkg.BuildParams{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown channel type")
	})
}

func TestRegistry_Provisioners(t *testing.T) {
	t.Run("includes types with provisioners", func(t *testing.T) {
		prov := &stubProvisioner{}
		reg := channelpkg.NewRegistry(
			&stubPackage{channelType: "with", prov: prov},
			&stubPackage{channelType: "without"},
		)

		provisioners := reg.Provisioners()
		require.Len(t, provisioners, 1)
		require.Same(t, prov, provisioners["with"])
	})

	t.Run("empty when no provisioners", func(t *testing.T) {
		reg := channelpkg.NewRegistry(&stubPackage{channelType: "none"})

		provisioners := reg.Provisioners()
		require.Empty(t, provisioners)
	})
}

func TestRegistry_DuplicateTypePanics(t *testing.T) {
	require.Panics(t, func() {
		channelpkg.NewRegistry(
			&stubPackage{channelType: "dup"},
			&stubPackage{channelType: "dup"},
		)
	})
}

// --- helpers ---

type stubPackage struct {
	channelType channel.ChannelType
	prov        channel.EphemeralProvisioner
}

func (s *stubPackage) Type() channel.ChannelType { return s.channelType }

func (s *stubPackage) Build(_ context.Context, _ channelpkg.BuildParams) (channel.Channel, error) {
	return &stubChannel{channelType: s.channelType}, nil
}

func (s *stubPackage) Provisioner() channel.EphemeralProvisioner { return s.prov }

type stubChannel struct {
	channelType channel.ChannelType
}

func (s *stubChannel) Info() channel.Info {
	return channel.Info{Type: s.channelType}
}
func (s *stubChannel) Messages(_ context.Context) <-chan string { return nil }
func (s *stubChannel) Send(_ context.Context, _ string) (channel.MessageID, error) {
	return "", nil
}
func (s *stubChannel) Edit(_ context.Context, _ channel.MessageID, _ string) error { return nil }
func (s *stubChannel) Done(_ context.Context) error                                { return nil }
func (s *stubChannel) SplitStatusMessages() bool                                   { return false }
func (s *stubChannel) Markup() channel.Markup                                      { return channel.MarkupMarkdown }
func (s *stubChannel) StatusWrap() channel.StatusWrap                              { return channel.StatusWrap{} }

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
