package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/config"
)

func TestWriterRemoveChannel(t *testing.T) {
	t.Run("removes the channel and strips links that pointed at it", func(t *testing.T) {
		w := newWriter(t, `prod:
  users:
    - id: theo
      channels:
        - type: telegram
          name: alpha
        - type: telegram
          name: beta
          links:
            - target: alpha
              description: talk to alpha
            - target: gamma
              description: talk to gamma
        - type: telegram
          name: gamma
`)
		require.NoError(t, w.RemoveChannel("theo", "gamma"))

		channels, err := w.ReadChannels("theo")
		require.NoError(t, err)
		require.Equal(t, []string{"alpha", "beta"}, channelNames(channels))

		// beta's dead link to gamma must be gone, its live link to alpha kept.
		beta := findChannel(t, channels, "beta")
		require.Len(t, beta.Links, 1)
		require.Equal(t, "alpha", beta.Links[0].Target)
	})

	t.Run("leaves channels with no link to the removed one untouched", func(t *testing.T) {
		w := newWriter(t, `prod:
  users:
    - id: theo
      channels:
        - type: telegram
          name: alpha
        - type: telegram
          name: beta
          links:
            - target: alpha
              description: talk to alpha
        - type: telegram
          name: gamma
`)
		require.NoError(t, w.RemoveChannel("theo", "gamma"))

		channels, err := w.ReadChannels("theo")
		require.NoError(t, err)
		beta := findChannel(t, channels, "beta")
		require.Len(t, beta.Links, 1)
		require.Equal(t, "alpha", beta.Links[0].Target)
	})

	t.Run("returns an error when the channel does not exist", func(t *testing.T) {
		w := newWriter(t, `prod:
  users:
    - id: theo
      channels:
        - type: telegram
          name: alpha
`)
		err := w.RemoveChannel("theo", "missing")
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
	})
}

// --- helpers ---

func newWriter(t *testing.T, yaml string) *config.Writer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tclaw.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))
	return config.NewWriter(path, config.EnvProd)
}

func channelNames(channels []config.Channel) []string {
	names := make([]string, len(channels))
	for i, ch := range channels {
		names[i] = ch.Name
	}
	return names
}

func findChannel(t *testing.T, channels []config.Channel, name string) config.Channel {
	t.Helper()
	for _, ch := range channels {
		if ch.Name == name {
			return ch
		}
	}
	t.Fatalf("channel %q not found", name)
	return config.Channel{}
}
