package reconciler_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/config"
	"tclaw/internal/libraries/store"
	"tclaw/internal/reconciler"
)

func TestReconcile(t *testing.T) {
	t.Run("provisions an unready channel by default", func(t *testing.T) {
		prov := &stubProvisioner{ready: false}
		results := reconcile(t, prov, false, config.Channel{Type: channel.TypeTelegram, Name: "alpha"})

		require.True(t, prov.provisioned, "expected Provision to be called")
		require.Len(t, results, 1)
		require.Equal(t, reconciler.ChannelReady, results[0].Status)
	})

	t.Run("does not provision when SkipProvision is set", func(t *testing.T) {
		prov := &stubProvisioner{ready: false}
		results := reconcile(t, prov, true, config.Channel{Type: channel.TypeTelegram, Name: "alpha"})

		require.False(t, prov.provisioned, "Provision must not be called when the config did not reload")
		require.Len(t, results, 1)
		require.Equal(t, reconciler.ChannelNeedsSetup, results[0].Status)
	})

	t.Run("an already-ready channel stays ready even when SkipProvision is set", func(t *testing.T) {
		prov := &stubProvisioner{ready: true}
		results := reconcile(t, prov, true, config.Channel{Type: channel.TypeTelegram, Name: "alpha"})

		require.False(t, prov.provisioned)
		require.Len(t, results, 1)
		require.Equal(t, reconciler.ChannelReady, results[0].Status)
	})
}

// --- helpers ---

func reconcile(t *testing.T, prov channel.EphemeralProvisioner, skipProvision bool, channels ...config.Channel) []reconciler.ReconciledChannel {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	results, err := reconciler.Reconcile(context.Background(), reconciler.ReconcileParams{
		Channels:      channels,
		RuntimeState:  channel.NewRuntimeStateStore(s),
		Provisioners:  func(channel.ChannelType) channel.EphemeralProvisioner { return prov },
		SkipProvision: skipProvision,
	})
	require.NoError(t, err)
	return results
}

// stubProvisioner records whether Provision was called so tests can assert that
// SkipProvision genuinely withholds platform-resource creation.
type stubProvisioner struct {
	ready       bool
	provisioned bool
}

func (s *stubProvisioner) IsReady(context.Context, string) bool { return s.ready }
func (s *stubProvisioner) CanAutoProvision() bool               { return true }
func (s *stubProvisioner) ValidateCreate(string) error          { return nil }

func (s *stubProvisioner) Provision(context.Context, channel.ProvisionParams) (*channel.ProvisionResult, error) {
	s.provisioned = true
	return &channel.ProvisionResult{}, nil
}

func (s *stubProvisioner) Teardown(context.Context, channel.TeardownState) error { return nil }

func (s *stubProvisioner) SendTeardownPrompt(context.Context, string, channel.PlatformState) error {
	return nil
}

func (s *stubProvisioner) SendClosingMessage(context.Context, string, channel.PlatformState) error {
	return nil
}

func (s *stubProvisioner) Notify(context.Context, string, string) error { return nil }

func (s *stubProvisioner) PlatformResponseInfo(channel.TeardownState) map[string]any { return nil }
