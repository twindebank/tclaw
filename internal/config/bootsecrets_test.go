package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/config"
)

func TestBootSecretRefs(t *testing.T) {
	// Both environments declare TELEGRAM_BOT_TOKEN with different values —
	// the dev bot and the production bot — which is exactly why the scan must
	// be scoped rather than run over the whole file.
	const sample = `
local:
    base_dir: /tmp/tclaw
    users:
        - id: alice
          channels:
            - type: telegram
              name: telegram-local
              telegram:
                token: ${boot:TELEGRAM_BOT_TOKEN}
            - type: telegram
              name: dev-only
              telegram:
                token: ${boot:LOCAL_ONLY_SECRET}
prod:
    base_dir: /data/tclaw
    egress_proxy:
        token: ${boot:EGRESS_TOKEN}
    users:
        - id: alice
          channels:
            - type: telegram
              name: admin
              telegram:
                token: ${boot:TELEGRAM_BOT_TOKEN}
            - type: telegram
              name: assistant
              telegram:
                token: ${boot:TELEGRAM_ASSISTANT_TOKEN}
`

	t.Run("returns only the requested environment's refs", func(t *testing.T) {
		path := writeConfig(t, sample)

		got, err := config.BootSecretRefs(path, config.EnvProd)
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]string{"EGRESS_TOKEN", "TELEGRAM_BOT_TOKEN", "TELEGRAM_ASSISTANT_TOKEN"}, got)
		require.NotContains(t, got, "LOCAL_ONLY_SECRET",
			"a local-only secret must never be pushed to prod")
	})

	t.Run("scopes to local too", func(t *testing.T) {
		path := writeConfig(t, sample)

		got, err := config.BootSecretRefs(path, config.EnvLocal)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"TELEGRAM_BOT_TOKEN", "LOCAL_ONLY_SECRET"}, got)
		require.NotContains(t, got, "EGRESS_TOKEN")
	})

	t.Run("deduplicates repeated refs", func(t *testing.T) {
		path := writeConfig(t, `
prod:
    a: ${boot:SHARED}
    b: ${boot:SHARED}
    c: ${boot:OTHER}
`)
		got, err := config.BootSecretRefs(path, config.EnvProd)
		require.NoError(t, err)
		require.Equal(t, []string{"SHARED", "OTHER"}, got)
	})

	t.Run("returns empty when the environment declares none", func(t *testing.T) {
		path := writeConfig(t, "prod:\n    base_dir: /data/tclaw\n")

		got, err := config.BootSecretRefs(path, config.EnvProd)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("errors on an unknown environment", func(t *testing.T) {
		path := writeConfig(t, "prod:\n    base_dir: /data/tclaw\n")

		_, err := config.BootSecretRefs(path, config.Env("staging"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found in config")
	})

	t.Run("errors on a missing file", func(t *testing.T) {
		_, err := config.BootSecretRefs(filepath.Join(t.TempDir(), "nope.yaml"), config.EnvProd)
		require.Error(t, err)
		require.Contains(t, err.Error(), "read config")
	})

	t.Run("errors on malformed yaml", func(t *testing.T) {
		path := writeConfig(t, "prod:\n  - this: [is\n   broken")

		_, err := config.BootSecretRefs(path, config.EnvProd)
		require.Error(t, err)
		require.Contains(t, err.Error(), "parse config")
	})
}

// --- helpers ---

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tclaw.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}
