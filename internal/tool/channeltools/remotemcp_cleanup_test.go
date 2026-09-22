package channeltools_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/config"
	"tclaw/internal/reconciler"
	"tclaw/internal/remotemcpstore"
	"tclaw/internal/tool/channeltools"
)

// --- helpers ---
// Scaffolding for the remote MCP cleanup subtests, which live under
// TestChannelDelete and TestChannelDone in channeltools_test.go.

func setupChannelDeleteWithRemoteMCPs(t *testing.T) (*channelDeleteHarness, *remotemcpstore.Manager) {
	t.Helper()
	h := buildDeleteHarness(t)
	mcpMgr := remotemcpstore.NewManager(
		mustFSStore(t, filepath.Join(h.userDir, "remote-mcp-state")),
		newMemorySecretStore(),
	)

	channeltools.RegisterTools(h.handler, channeltools.Deps{
		Registry:     h.registry,
		ConfigWriter: h.configWriter,
		RuntimeState: h.runtimeState,
		UserID:       testUserID,
		Env:          config.EnvLocal,
		SecretStore:  newMemorySecretStore(),
		RemoteMCPs:   mcpMgr,
		ReconcileParams: reconciler.ReconcileParams{
			RuntimeState: h.runtimeState,
		},
	})
	return h, mcpMgr
}

// setupChannelDeleteWithoutRemoteMCPs registers the tools with Deps.RemoteMCPs
// left nil, which is what the one-shot CLI does.
func setupChannelDeleteWithoutRemoteMCPs(t *testing.T) *channelDeleteHarness {
	t.Helper()
	h := buildDeleteHarness(t)

	channeltools.RegisterTools(h.handler, channeltools.Deps{
		Registry:     h.registry,
		ConfigWriter: h.configWriter,
		RuntimeState: h.runtimeState,
		UserID:       testUserID,
		Env:          config.EnvLocal,
		SecretStore:  newMemorySecretStore(),
		ReconcileParams: reconciler.ReconcileParams{
			RuntimeState: h.runtimeState,
		},
	})
	return h
}

// doneSeededChannel creates the named channel and tears it down through
// channel_done. A socket channel has no platform chat, so teardown runs at once.
func doneSeededChannel(t *testing.T, h *channelDeleteHarness, name string) map[string]any {
	t.Helper()
	require.NoError(t, h.configWriter.AddChannel(testUserID, config.Channel{
		Name:      name,
		Type:      channel.TypeSocket,
		Ephemeral: true,
	}))
	reloadRegistryForDeleteTest(t, h)

	result := callTool(t, h.handler, "channel_done", map[string]any{
		"channel_name": name,
		"results_sent": "done",
	})
	var resp map[string]any
	require.NoError(t, json.Unmarshal(result, &resp))
	return resp
}

func addRemoteMCPForDeleteTest(t *testing.T, mgr *remotemcpstore.Manager, name string, channels ...string) {
	t.Helper()
	_, err := mgr.AddRemoteMCP(context.Background(), remotemcpstore.AddRemoteMCPParams{
		Name: name, URL: "https://" + name + ".example.com/mcp", Channels: channels,
	})
	require.NoError(t, err)
}

// deleteSeededChannel creates the named channel and deletes it through the tool,
// returning the tool's response.
func deleteSeededChannel(t *testing.T, h *channelDeleteHarness, name string) map[string]any {
	t.Helper()
	require.NoError(t, h.configWriter.AddChannel(testUserID, config.Channel{
		Name:      name,
		Type:      channel.TypeSocket,
		Ephemeral: true,
	}))
	reloadRegistryForDeleteTest(t, h)

	result := callTool(t, h.handler, "channel_delete", map[string]any{"name": name})
	var resp map[string]any
	require.NoError(t, json.Unmarshal(result, &resp))
	return resp
}
