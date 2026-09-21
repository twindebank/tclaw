package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/config"
	"tclaw/internal/libraries/store"
	"tclaw/internal/remotemcpstore"
)

func TestCheckRemoteMCPScope(t *testing.T) {
	t.Run("flags nothing when every named channel exists", func(t *testing.T) {
		mgr, cw := setupScopeTest(t, "assistant", "shopping")
		addScopedRemoteMCP(t, mgr, "browser", "assistant", "shopping")

		flagged, err := checkRemoteMCPScope(context.Background(), mgr, cw, testUserID)
		require.NoError(t, err)
		require.Empty(t, flagged)
	})

	t.Run("flags a registration naming a channel that is gone", func(t *testing.T) {
		// A wholesale config rewrite can drop a channel without any teardown
		// path running, which is what this backstops.
		mgr, cw := setupScopeTest(t, "assistant")
		addScopedRemoteMCP(t, mgr, "browser", "assistant", "reaped")
		addScopedRemoteMCP(t, mgr, "house", "assistant")

		flagged, err := checkRemoteMCPScope(context.Background(), mgr, cw, testUserID)
		require.NoError(t, err)
		require.Equal(t, []string{"browser"}, flagged)
	})

	t.Run("flags a registration that names no channel at all", func(t *testing.T) {
		mgr, cw := setupScopeTest(t, "assistant")
		addScopedRemoteMCP(t, mgr, "browser")

		flagged, err := checkRemoteMCPScope(context.Background(), mgr, cw, testUserID)
		require.NoError(t, err)
		require.Equal(t, []string{"browser"}, flagged,
			"an empty list reaches every channel, which no tool can create")
	})

	t.Run("errors when the channels cannot be read", func(t *testing.T) {
		mgr, _ := setupScopeTest(t, "assistant")
		missing := config.NewWriter(filepath.Join(t.TempDir(), "absent.yaml"), config.EnvLocal)

		_, err := checkRemoteMCPScope(context.Background(), mgr, missing, testUserID)
		require.Error(t, err)
		require.Contains(t, err.Error(), "read channels")
	})
}

// --- helpers ---

func setupScopeTest(t *testing.T, channelNames ...string) (*remotemcpstore.Manager, *config.Writer) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "tclaw.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`local:
  users:
    - id: testuser
      channels: []
`), 0o644))

	cw := config.NewWriter(configPath, config.EnvLocal)
	for _, name := range channelNames {
		require.NoError(t, cw.AddChannel(testUserID, config.Channel{
			Name: name, Type: channel.TypeSocket,
		}))
	}

	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return remotemcpstore.NewManager(s, newMemDoneSecretStore()), cw
}

func addScopedRemoteMCP(t *testing.T, mgr *remotemcpstore.Manager, name string, channels ...string) {
	t.Helper()
	_, err := mgr.AddRemoteMCP(context.Background(), remotemcpstore.AddRemoteMCPParams{
		Name: name, URL: "https://" + name + ".example.com/mcp", Channels: channels,
	})
	require.NoError(t, err)
}
