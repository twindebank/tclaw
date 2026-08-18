package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/config"
)

func TestLoad_EgressProxySecretResolution(t *testing.T) {
	t.Run("resolves the egress proxy token from the environment", func(t *testing.T) {
		t.Setenv("TEST_EGRESS_TOKEN", "the-real-token")
		path := writeEnvConfig(t, `
prod:
    base_dir: /data/tclaw
    egress_proxy:
        url: "http://egress.internal:8000"
        token: ${boot:TEST_EGRESS_TOKEN}
        hosts:
            - mcp.example.com
    users:
        - id: alice
          channels:
            - type: socket
              name: admin
              description: test channel
              tool_groups: [all_tools]
`)

		cfg, err := config.Load(path, config.EnvProd)
		require.NoError(t, err)
		require.NotNil(t, cfg.EgressProxy)
		// The whole point: a literal "${boot:...}" here is non-empty and would
		// sail past any is-it-set check, then be sent as the credential.
		require.Equal(t, "the-real-token", cfg.EgressProxy.Token)
	})

	t.Run("fails when a boot reference cannot be resolved", func(t *testing.T) {
		path := writeEnvConfig(t, `
prod:
    base_dir: /data/tclaw
    egress_proxy:
        url: "http://egress.internal:8000"
        token: ${boot:DEFINITELY_NOT_SET_ANYWHERE}
        hosts:
            - mcp.example.com
    users:
        - id: alice
          channels:
            - type: socket
              name: admin
              description: test channel
              tool_groups: [all_tools]
`)

		_, err := config.Load(path, config.EnvProd)
		require.Error(t, err)
		require.Contains(t, err.Error(), "DEFINITELY_NOT_SET_ANYWHERE",
			"must fail on the unresolved secret, not on unrelated validation")
	})

	t.Run("a config with no egress proxy still loads", func(t *testing.T) {
		path := writeEnvConfig(t, `
prod:
    base_dir: /data/tclaw
    users:
        - id: alice
          channels:
            - type: socket
              name: admin
              description: test channel
              tool_groups: [all_tools]
`)

		cfg, err := config.Load(path, config.EnvProd)
		require.NoError(t, err)
		require.Nil(t, cfg.EgressProxy)
	})
}

// --- helpers ---

func writeEnvConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tclaw.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}
