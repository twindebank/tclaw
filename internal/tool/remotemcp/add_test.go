package remotemcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/remotemcpstore"
	"tclaw/internal/tool/remotemcp"
)

func TestRemoteMCPAdd_SkipAuthDiscoveryWithHeaders(t *testing.T) {
	t.Run("stores static headers and reaches ready state", func(t *testing.T) {
		h, mgr, updated := setup(t)

		result := callTool(t, h, "remote_mcp_add", map[string]any{
			"name":                "home-assistant",
			"url":                 "https://ha-mcp.example.com/mcp_abc",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers": map[string]string{
				"CF-Access-Client-Id":     "client-id",
				"CF-Access-Client-Secret": "client-secret",
			},
		})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "ready", got["status"])
		require.Equal(t, "home-assistant", got["name"])
		require.Equal(t, "https://ha-mcp.example.com", got["url"], "url should be redacted to scheme+host in response")

		auth, err := mgr.GetRemoteMCPAuth(context.Background(), "home-assistant")
		require.NoError(t, err)
		require.NotNil(t, auth)
		require.Equal(t, "client-id", auth.StaticHeaders["CF-Access-Client-Id"])
		require.Equal(t, "client-secret", auth.StaticHeaders["CF-Access-Client-Secret"])
		require.Empty(t, auth.AccessToken)

		require.Equal(t, 1, *updated, "config updater should fire once after add")
	})

	t.Run("skip_auth_discovery without headers still persists entry", func(t *testing.T) {
		h, mgr, _ := setup(t)

		_ = callTool(t, h, "remote_mcp_add", map[string]any{
			"name":                "open-mcp",
			"url":                 "https://open.example.com/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
		})

		mcps, err := mgr.ListRemoteMCPs(context.Background())
		require.NoError(t, err)
		require.Len(t, mcps, 1)

		// No auth entry should be stored when no headers were supplied.
		auth, err := mgr.GetRemoteMCPAuth(context.Background(), "open-mcp")
		require.NoError(t, err)
		require.Nil(t, auth)
	})

	t.Run("rejects headers without skip_auth_discovery", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "remote_mcp_add", map[string]any{
			"name":    "bad",
			"url":     "https://example.com/mcp",
			"channel": "desktop",
			"headers": map[string]string{"X-Foo": "bar"},
		})
		require.Contains(t, err.Error(), "skip_auth_discovery=true")
	})

	t.Run("rejects invalid header name", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "remote_mcp_add", map[string]any{
			"name":                "bad",
			"url":                 "https://example.com/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers":             map[string]string{"bad header": "value"},
		})
		require.Contains(t, err.Error(), "invalid header name")
	})

	t.Run("rejects empty header value", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "remote_mcp_add", map[string]any{
			"name":                "bad",
			"url":                 "https://example.com/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers":             map[string]string{"X-Foo": ""},
		})
		require.Contains(t, err.Error(), "invalid header value")
	})

	t.Run("rejects CRLF injection in header value", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "remote_mcp_add", map[string]any{
			"name":                "bad",
			"url":                 "https://example.com/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers":             map[string]string{"X-Foo": "ok\r\nX-Evil: injected"},
		})
		require.Contains(t, err.Error(), "control character")
	})
}

// --- helpers ---

func setup(t *testing.T) (*mcp.Handler, *remotemcpstore.Manager, *int) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	secrets := &memorySecretStore{data: map[string]string{}}
	mgr := remotemcpstore.NewManager(s, secrets)

	updateCount := 0
	handler := mcp.NewHandler()
	remotemcp.RegisterTools(handler, remotemcp.Deps{
		Manager: mgr,
		ConfigUpdater: func(_ context.Context) error {
			updateCount++
			return nil
		},
	})

	return handler, mgr, &updateCount
}

func callTool(t *testing.T, h *mcp.Handler, name string, args any) json.RawMessage {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	result, err := h.Call(context.Background(), name, argsJSON)
	require.NoError(t, err, "call %s", name)
	return result
}

func callToolExpectError(t *testing.T, h *mcp.Handler, name string, args any) error {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = h.Call(context.Background(), name, argsJSON)
	require.Error(t, err, "expected error from %s", name)
	return err
}

type memorySecretStore struct {
	data map[string]string
}

var _ secret.Store = (*memorySecretStore)(nil)

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
