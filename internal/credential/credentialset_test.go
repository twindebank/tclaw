package credential_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/credential"
)

func TestGitTokenKey(t *testing.T) {
	t.Run("addresses the named slot's token field", func(t *testing.T) {
		require.Equal(t, "cred/git/homeassistant/token", credential.GitTokenKey("homeassistant"))
	})

	t.Run("falls back to the default slot", func(t *testing.T) {
		// A repo that names no credential of its own shares the default token.
		require.Equal(t, "cred/git/default/token", credential.GitTokenKey(""))
	})

	t.Run("stays inside the credential namespace", func(t *testing.T) {
		// Agent-facing key validation forbids slashes, so a key under cred/ is
		// unreachable by anything the agent can name.
		require.Contains(t, credential.GitTokenKey("default"), "/")
	})
}
