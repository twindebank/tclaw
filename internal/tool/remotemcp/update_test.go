package remotemcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteMCPUpdate(t *testing.T) {
	t.Run("replaces the channel list", func(t *testing.T) {
		th := addTestServer(t, "browser-mcp", []string{"desktop"})

		result := callTool(t, th.handler, "remote_mcp_update", map[string]any{
			"name":     "browser-mcp",
			"channels": []string{"desktop", "shopping"},
		})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, []any{"desktop", "shopping"}, got["channels"])

		entry, err := th.manager.GetRemoteMCP(context.Background(), "browser-mcp")
		require.NoError(t, err)
		require.Equal(t, []string{"desktop", "shopping"}, entry.Channels)
	})

	t.Run("restarts the agent so the new channel picks the tools up", func(t *testing.T) {
		th := addTestServer(t, "browser-mcp", []string{"desktop"})
		before := *th.channelChangeCount

		_ = callTool(t, th.handler, "remote_mcp_update", map[string]any{
			"name":     "browser-mcp",
			"channels": []string{"shopping"},
		})

		require.Equal(t, before+1, *th.channelChangeCount,
			"the CLI reads its tool allowlist at process start, so the change needs a restart")
	})

	t.Run("dropping a server's last channel is refused", func(t *testing.T) {
		th := addTestServer(t, "browser-mcp", []string{"desktop"})

		err := callToolExpectError(t, th.handler, "remote_mcp_update", map[string]any{
			"name":     "browser-mcp",
			"channels": []string{},
		})
		require.Contains(t, err.Error(), "remote_mcp_remove")

		entry, getErr := th.manager.GetRemoteMCP(context.Background(), "browser-mcp")
		require.NoError(t, getErr)
		require.Equal(t, []string{"desktop"}, entry.Channels,
			"an empty list means every channel, so the stored scope must be left alone")
	})

	t.Run("rejects a channel listed twice", func(t *testing.T) {
		th := addTestServer(t, "browser-mcp", []string{"desktop"})

		err := callToolExpectError(t, th.handler, "remote_mcp_update", map[string]any{
			"name":     "browser-mcp",
			"channels": []string{"shopping", "shopping"},
		})
		require.Contains(t, err.Error(), "listed twice")
	})

	t.Run("a mistyped channel leaves the scope alone", func(t *testing.T) {
		th := addTestServer(t, "browser-mcp", []string{"desktop"})

		err := callToolExpectError(t, th.handler, "remote_mcp_update", map[string]any{
			"name":     "browser-mcp",
			"channels": []string{"desktop", "shoping"},
		})
		require.Contains(t, err.Error(), `no channel named "shoping"`)

		entry, getErr := th.manager.GetRemoteMCP(context.Background(), "browser-mcp")
		require.NoError(t, getErr)
		require.Equal(t, []string{"desktop"}, entry.Channels,
			"a rejected update must not drop the channel that already had the server")
	})

	t.Run("a name left behind by a deleted channel can still be sent back", func(t *testing.T) {
		th := addTestServer(t, "browser-mcp", []string{"desktop", "shopping"})
		// The desktop channel is deleted; nothing prunes the registration.
		*th.channelNames = []string{"email", "running", "shopping"}

		_ = callTool(t, th.handler, "remote_mcp_update", map[string]any{
			"name":     "browser-mcp",
			"channels": []string{"desktop", "shopping", "email"},
		})

		entry, err := th.manager.GetRemoteMCP(context.Background(), "browser-mcp")
		require.NoError(t, err)
		require.Equal(t, []string{"desktop", "shopping", "email"}, entry.Channels,
			"a stale name must not block every later edit of the registration")
	})

	t.Run("one stale name can be dropped while another is kept", func(t *testing.T) {
		th := addTestServer(t, "browser-mcp", []string{"desktop", "email", "shopping"})
		// Both desktop and email are deleted; the registration still names them.
		*th.channelNames = []string{"shopping", "running"}

		_ = callTool(t, th.handler, "remote_mcp_update", map[string]any{
			"name":     "browser-mcp",
			"channels": []string{"desktop", "shopping"},
		})

		entry, err := th.manager.GetRemoteMCP(context.Background(), "browser-mcp")
		require.NoError(t, err)
		require.Equal(t, []string{"desktop", "shopping"}, entry.Channels,
			"email is dropped by being left out; desktop is kept although no channel has it")
	})

	t.Run("says which kept names reach nothing", func(t *testing.T) {
		th := addTestServer(t, "browser-mcp", []string{"desktop", "shopping"})
		*th.channelNames = []string{"shopping", "running"}

		result := callTool(t, th.handler, "remote_mcp_update", map[string]any{
			"name":     "browser-mcp",
			"channels": []string{"desktop", "shopping"},
		})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, []any{"desktop"}, got["channels_matching_nothing"])
		require.Contains(t, got["message"], "reach nothing",
			"a dead name must not be reported as live scope")
		require.Contains(t, got["message"], "1 channel(s)",
			"the count should leave out the name that reaches nothing")
	})

	t.Run("reports every bad name at once", func(t *testing.T) {
		th := addTestServer(t, "browser-mcp", []string{"desktop"})

		err := callToolExpectError(t, th.handler, "remote_mcp_update", map[string]any{
			"name":     "browser-mcp",
			"channels": []string{"shoping", "emial"},
		})
		require.Contains(t, err.Error(), `"shoping"`)
		require.Contains(t, err.Error(), `"emial"`,
			"a list with several typos should take one correction, not one round trip each")
	})

	t.Run("rejects a call that changes nothing", func(t *testing.T) {
		th := addTestServer(t, "browser-mcp", []string{"desktop"})

		err := callToolExpectError(t, th.handler, "remote_mcp_update", map[string]any{
			"name": "browser-mcp",
		})
		require.Contains(t, err.Error(), "nothing to update")
	})

	t.Run("errors on an unknown server", func(t *testing.T) {
		th := setupHarness(t)

		err := callToolExpectError(t, th.handler, "remote_mcp_update", map[string]any{
			"name":     "ghost",
			"channels": []string{"desktop"},
		})
		require.Contains(t, err.Error(), "not found")
	})
}

// --- helpers ---

// addTestServer registers a server through remote_mcp_add so the update tests
// start from a registration the tool itself produced.
func addTestServer(t *testing.T, name string, channels []string) *testHarness {
	t.Helper()
	server := fakeMCPServer(t, []string{"browser_navigate"})
	th := setupHarness(t, withHTTPClient(server.Client()))
	_ = callTool(t, th.handler, "remote_mcp_add", map[string]any{
		"name":                name,
		"url":                 server.URL + "/mcp",
		"channels":            channels,
		"skip_auth_discovery": true,
	})
	return th
}
