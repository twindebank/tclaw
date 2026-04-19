package remotemcp_test

import (
	"context"
	"encoding/json"
	"net/http"
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
		server := fakeMCPServer(t, []string{"ha_tool_a", "ha_tool_b"})
		h, mgr, updated := setup(t, withHTTPClient(server.Client()))

		result := callTool(t, h, "remote_mcp_add", map[string]any{
			"name":                "home-assistant",
			"url":                 server.URL + "/mcp_abc",
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
		require.Equal(t, false, got["url_is_secret"], "inline url is not sensitive")
		require.Equal(t, server.URL+"/mcp_abc", got["url"], "full url present for inline registration")

		auth, err := mgr.GetRemoteMCPAuth(context.Background(), "home-assistant")
		require.NoError(t, err)
		require.NotNil(t, auth)
		require.Equal(t, "client-id", auth.StaticHeaders["CF-Access-Client-Id"])
		require.Equal(t, "client-secret", auth.StaticHeaders["CF-Access-Client-Secret"])
		require.Empty(t, auth.AccessToken)

		entry, err := mgr.GetRemoteMCP(context.Background(), "home-assistant")
		require.NoError(t, err)
		require.Equal(t, []string{"ha_tool_a", "ha_tool_b"}, entry.ToolNames,
			"tool names from server should be persisted")

		require.Equal(t, 1, *updated, "config updater should fire once after add")
	})

	t.Run("skip_auth_discovery without headers still persists entry", func(t *testing.T) {
		server := fakeMCPServer(t, []string{"open_tool"})
		h, mgr, _ := setup(t, withHTTPClient(server.Client()))

		_ = callTool(t, h, "remote_mcp_add", map[string]any{
			"name":                "open-mcp",
			"url":                 server.URL + "/mcp",
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

func TestRemoteMCPAdd_HeaderSecretKeys(t *testing.T) {
	t.Run("resolves values from secret store and stores as static headers", func(t *testing.T) {
		server := fakeMCPServer(t, []string{"ha_one", "ha_two"})
		th := setupHarness(t, withHTTPClient(server.Client()))
		th.secrets.data["ha_mcp_cf_access_client_id"] = "client-id-from-store"
		th.secrets.data["ha_mcp_cf_access_client_secret"] = "client-secret-from-store"

		result := callTool(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "home-assistant",
			"url":                 server.URL + "/mcp_abc",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"header_secret_keys": map[string]string{
				"CF-Access-Client-Id":     "ha_mcp_cf_access_client_id",
				"CF-Access-Client-Secret": "ha_mcp_cf_access_client_secret",
			},
		})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "ready", got["status"])

		auth, err := th.manager.GetRemoteMCPAuth(context.Background(), "home-assistant")
		require.NoError(t, err)
		require.NotNil(t, auth)
		require.Equal(t, "client-id-from-store", auth.StaticHeaders["CF-Access-Client-Id"])
		require.Equal(t, "client-secret-from-store", auth.StaticHeaders["CF-Access-Client-Secret"])
	})

	t.Run("combines inline headers with secret-resolved headers", func(t *testing.T) {
		server := fakeMCPServer(t, []string{"combo_tool"})
		th := setupHarness(t, withHTTPClient(server.Client()))
		th.secrets.data["tenant_token"] = "resolved-value"

		_ = callTool(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "combo",
			"url":                 server.URL + "/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers": map[string]string{
				"X-Tenant": "acme",
			},
			"header_secret_keys": map[string]string{
				"X-Auth-Token": "tenant_token",
			},
		})

		auth, err := th.manager.GetRemoteMCPAuth(context.Background(), "combo")
		require.NoError(t, err)
		require.Equal(t, "acme", auth.StaticHeaders["X-Tenant"])
		require.Equal(t, "resolved-value", auth.StaticHeaders["X-Auth-Token"])
	})

	t.Run("errors clearly when referenced secret is missing", func(t *testing.T) {
		th := setupHarness(t)

		err := callToolExpectError(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "bad",
			"url":                 "https://example.com/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"header_secret_keys": map[string]string{
				"CF-Access-Client-Id": "missing_key",
			},
		})
		require.Contains(t, err.Error(), "missing_key")
		require.Contains(t, err.Error(), "secret_form_request")
	})

	t.Run("error message does not leak the secret value on unset key", func(t *testing.T) {
		th := setupHarness(t)

		err := callToolExpectError(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "bad",
			"url":                 "https://example.com/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"header_secret_keys": map[string]string{
				"CF-Access-Client-Secret": "never_set",
			},
		})
		// The error references the header name and key but not any value.
		require.Contains(t, err.Error(), "CF-Access-Client-Secret")
		require.Contains(t, err.Error(), "never_set")
	})

	t.Run("rejects duplicate header across inline and secret_keys", func(t *testing.T) {
		th := setupHarness(t)
		th.secrets.data["key1"] = "value-from-secret"

		err := callToolExpectError(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "bad",
			"url":                 "https://example.com/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers": map[string]string{
				"X-Foo": "inline-value",
			},
			"header_secret_keys": map[string]string{
				"X-Foo": "key1",
			},
		})
		require.Contains(t, err.Error(), "choose one source")
	})

	t.Run("rejects secret headers without skip_auth_discovery", func(t *testing.T) {
		th := setupHarness(t)
		th.secrets.data["k"] = "v"

		err := callToolExpectError(t, th.handler, "remote_mcp_add", map[string]any{
			"name":    "bad",
			"url":     "https://example.com/mcp",
			"channel": "desktop",
			"header_secret_keys": map[string]string{
				"X-Foo": "k",
			},
		})
		require.Contains(t, err.Error(), "skip_auth_discovery=true")
	})

	t.Run("rejects empty secret key", func(t *testing.T) {
		th := setupHarness(t)

		err := callToolExpectError(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "bad",
			"url":                 "https://example.com/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"header_secret_keys": map[string]string{
				"X-Foo": "",
			},
		})
		require.Contains(t, err.Error(), "empty")
	})
}

func TestRemoteMCPAdd_URLSecretKey(t *testing.T) {
	t.Run("resolves URL from secret store", func(t *testing.T) {
		server := fakeMCPServer(t, []string{"secret_tool"})
		th := setupHarness(t, withHTTPClient(server.Client()))
		th.secrets.data["mcp_url"] = server.URL + "/abc_secret_xyz"

		_ = callTool(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "private",
			"url_secret_key":      "mcp_url",
			"channel":             "desktop",
			"skip_auth_discovery": true,
		})

		mcps, err := th.manager.ListRemoteMCPs(context.Background())
		require.NoError(t, err)
		require.Len(t, mcps, 1)
		require.Equal(t, server.URL+"/abc_secret_xyz", mcps[0].URL,
			"resolved URL should be stored as the remote MCP URL")
	})

	t.Run("combines url_secret_key with header_secret_keys", func(t *testing.T) {
		server := fakeMCPServer(t, []string{"full_tool"})
		th := setupHarness(t, withHTTPClient(server.Client()))
		th.secrets.data["mcp_url"] = server.URL + "/abc_xyz"
		th.secrets.data["cf_id"] = "cf-client-id"
		th.secrets.data["cf_secret"] = "cf-client-secret"

		_ = callTool(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "full-secret",
			"url_secret_key":      "mcp_url",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"header_secret_keys": map[string]string{
				"CF-Access-Client-Id":     "cf_id",
				"CF-Access-Client-Secret": "cf_secret",
			},
		})

		mcps, err := th.manager.ListRemoteMCPs(context.Background())
		require.NoError(t, err)
		require.Equal(t, server.URL+"/abc_xyz", mcps[0].URL)

		auth, err := th.manager.GetRemoteMCPAuth(context.Background(), "full-secret")
		require.NoError(t, err)
		require.Equal(t, "cf-client-id", auth.StaticHeaders["CF-Access-Client-Id"])
		require.Equal(t, "cf-client-secret", auth.StaticHeaders["CF-Access-Client-Secret"])
	})

	t.Run("rejects when both url and url_secret_key provided", func(t *testing.T) {
		th := setupHarness(t)
		th.secrets.data["k"] = "https://other.example.com/"

		err := callToolExpectError(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "bad",
			"url":                 "https://inline.example.com/",
			"url_secret_key":      "k",
			"channel":             "desktop",
			"skip_auth_discovery": true,
		})
		require.Contains(t, err.Error(), "only one of url or url_secret_key")
	})

	t.Run("rejects when neither url nor url_secret_key provided", func(t *testing.T) {
		th := setupHarness(t)

		err := callToolExpectError(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "bad",
			"channel":             "desktop",
			"skip_auth_discovery": true,
		})
		require.Contains(t, err.Error(), "url or url_secret_key is required")
	})

	t.Run("rejects when referenced url secret is missing", func(t *testing.T) {
		th := setupHarness(t)

		err := callToolExpectError(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "bad",
			"url_secret_key":      "never_set_key",
			"channel":             "desktop",
			"skip_auth_discovery": true,
		})
		require.Contains(t, err.Error(), "never_set_key")
		require.Contains(t, err.Error(), "secret_form_request")
	})

	t.Run("validates resolved URL is https", func(t *testing.T) {
		th := setupHarness(t)
		th.secrets.data["bad_url"] = "http://not-tls.example.com/"

		err := callToolExpectError(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "bad",
			"url_secret_key":      "bad_url",
			"channel":             "desktop",
			"skip_auth_discovery": true,
		})
		require.Contains(t, err.Error(), "HTTPS")
	})

	t.Run("validates resolved URL is well-formed", func(t *testing.T) {
		th := setupHarness(t)
		th.secrets.data["bad_url"] = "not a url at all"

		err := callToolExpectError(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "bad",
			"url_secret_key":      "bad_url",
			"channel":             "desktop",
			"skip_auth_discovery": true,
		})
		require.Contains(t, err.Error(), "valid absolute URL")
	})
}

// --- helpers ---

type testHarness struct {
	handler            *mcp.Handler
	manager            *remotemcpstore.Manager
	secrets            *memorySecretStore
	updateCount        *int
	channelChangeCount *int
}

type harnessOpt func(*remotemcp.Deps)

// withHTTPClient plumbs a self-signed-trusting HTTP client into the tool's
// deps so ListTools can reach an httptest.NewTLSServer on 127.0.0.1.
func withHTTPClient(c *http.Client) harnessOpt {
	return func(d *remotemcp.Deps) { d.HTTPClient = c }
}

func setup(t *testing.T, opts ...harnessOpt) (*mcp.Handler, *remotemcpstore.Manager, *int) {
	th := setupHarness(t, opts...)
	return th.handler, th.manager, th.updateCount
}

func setupHarness(t *testing.T, opts ...harnessOpt) *testHarness {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	secrets := &memorySecretStore{data: map[string]string{}}
	mgr := remotemcpstore.NewManager(s, secrets)

	updateCount := 0
	channelChangeCount := 0
	deps := remotemcp.Deps{
		Manager:     mgr,
		SecretStore: secrets,
		ConfigUpdater: func(_ context.Context) error {
			updateCount++
			return nil
		},
		OnChannelChange: func() { channelChangeCount++ },
	}
	for _, opt := range opts {
		opt(&deps)
	}

	handler := mcp.NewHandler()
	remotemcp.RegisterTools(handler, deps)

	return &testHarness{
		handler:            handler,
		manager:            mgr,
		secrets:            secrets,
		updateCount:        &updateCount,
		channelChangeCount: &channelChangeCount,
	}
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
