package remotemcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/mcp"
	"tclaw/internal/remotemcpstore"
)

// TestAddToConfigFile covers the full path from remote_mcp_add tool call
// through to the generated --mcp-config JSON file on disk. This is the
// contract the router relies on: static headers stored via the tool must
// surface in the generated config so the Claude CLI sends them.
func TestAddToConfigFile(t *testing.T) {
	t.Run("static headers land in config file", func(t *testing.T) {
		server := fakeMCPServer(t, []string{"ha_state_get", "ha_service_call"})
		h, mgr, _ := setup(t, withHTTPClient(server.Client()))

		_ = callTool(t, h, "remote_mcp_add", map[string]any{
			"name":                "home-assistant",
			"url":                 server.URL + "/secret_path_abc",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers": map[string]string{
				"CF-Access-Client-Id":     "client-id.access",
				"CF-Access-Client-Secret": "super-secret",
			},
		})

		cfg := generateConfigFromManager(t, mgr)

		got, ok := cfg.MCPServers["home-assistant"]
		require.True(t, ok, "home-assistant entry missing from config")
		require.Equal(t, server.URL+"/secret_path_abc", got.URL)
		require.Equal(t, "client-id.access", got.Headers["CF-Access-Client-Id"])
		require.Equal(t, "super-secret", got.Headers["CF-Access-Client-Secret"])
		require.NotContains(t, got.Headers, "Authorization", "no OAuth in this flow")
	})

	t.Run("remove drops the server from the config", func(t *testing.T) {
		server := fakeMCPServer(t, []string{"ha_one"})
		h, mgr, _ := setup(t, withHTTPClient(server.Client()))

		_ = callTool(t, h, "remote_mcp_add", map[string]any{
			"name":                "home-assistant",
			"url":                 server.URL + "/secret",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers":             map[string]string{"CF-Access-Client-Id": "id"},
		})

		cfg := generateConfigFromManager(t, mgr)
		require.Contains(t, cfg.MCPServers, "home-assistant")

		_ = callTool(t, h, "remote_mcp_remove", map[string]any{
			"name": "home-assistant",
		})

		cfg = generateConfigFromManager(t, mgr)
		require.NotContains(t, cfg.MCPServers, "home-assistant", "removed MCP should not appear in config")
	})
}

// TestHeadersOnWire proves that the credentials in the generated config are
// faithfully transmitted on the HTTP request. This is the last contract
// before the Claude CLI takes over — if this test passes, any bug from here
// on is in the CLI, not in tclaw.
func TestHeadersOnWire(t *testing.T) {
	t.Run("static headers reach the remote server intact", func(t *testing.T) {
		server, receivedHeaders := withRecordedHeaders(t, []string{"ha_tool"})
		h, mgr, _ := setup(t, withHTTPClient(server.Client()))

		_ = callTool(t, h, "remote_mcp_add", map[string]any{
			"name":                "ha-wire",
			"url":                 server.URL + "/mcp_secret_path",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers": map[string]string{
				"CF-Access-Client-Id":     "client-id.access",
				"CF-Access-Client-Secret": "wire-test-secret",
			},
		})

		// Registration itself exercised the headers (initialize + tools/list
		// calls). Assert the server saw them on every call.
		records := receivedHeaders()
		require.GreaterOrEqual(t, len(records), 2,
			"expected at least initialize + tools/list on the fake server")
		for i, h := range records {
			require.Equal(t, "client-id.access", h.Get("CF-Access-Client-Id"),
				"request %d missing CF-Access-Client-Id header", i)
			require.Equal(t, "wire-test-secret", h.Get("CF-Access-Client-Secret"),
				"request %d missing CF-Access-Client-Secret header", i)
		}

		// And the generated config file carries the same values — proves the
		// registration-time wire contract also holds at runtime (when the CLI
		// reads the config and dials the origin on each turn).
		cfg := generateConfigFromManager(t, mgr)
		entry, ok := cfg.MCPServers["ha-wire"]
		require.True(t, ok)
		require.Equal(t, "client-id.access", entry.Headers["CF-Access-Client-Id"])
		require.Equal(t, "wire-test-secret", entry.Headers["CF-Access-Client-Secret"])
	})
}

// TestSecretRegistration_NoLeaksE2E is the belt-and-braces leak audit for
// the url_secret_key + header_secret_keys flow. It exercises the full pipe
// (secret form → remote_mcp_add → generated config → HTTP wire → list tool)
// and asserts every user-visible surface keeps sensitive values hidden,
// while the config file (consumed only by the CLI subprocess) contains the
// real values — that's a correctness requirement, not a leak.
func TestSecretRegistration_NoLeaksE2E(t *testing.T) {
	const (
		secretURLPath   = "/private_oh_so_sensitive_abc123"
		secretClientID  = "very-secret-client-id.access"
		secretClientSec = "super-secret-value-42"
		urlSecretKey    = "ha_mcp_url"
		idSecretKey     = "ha_mcp_cf_access_client_id"
		secretSecretKey = "ha_mcp_cf_access_client_secret"
	)

	server, receivedHeaders := withRecordedHeaders(t, []string{"ha_leak_probe_tool"})
	th := setupHarness(t, withHTTPClient(server.Client()))

	// Seed the secret store as if the user had filled in the secret form.
	th.secrets.data[urlSecretKey] = server.URL + secretURLPath
	th.secrets.data[idSecretKey] = secretClientID
	th.secrets.data[secretSecretKey] = secretClientSec

	// --- 1. remote_mcp_add must not echo secret values in its response ---
	addResult := callTool(t, th.handler, "remote_mcp_add", map[string]any{
		"name":                "home-assistant",
		"url_secret_key":      urlSecretKey,
		"channel":             "desktop",
		"skip_auth_discovery": true,
		"header_secret_keys": map[string]string{
			"CF-Access-Client-Id":     idSecretKey,
			"CF-Access-Client-Secret": secretSecretKey,
		},
	})

	addResultStr := string(addResult)
	assertNoSecretsInString(t, "remote_mcp_add response", addResultStr,
		secretURLPath, secretClientID, secretClientSec)

	var addParsed map[string]any
	require.NoError(t, json.Unmarshal(addResult, &addParsed))
	require.Equal(t, "ready", addParsed["status"])
	require.Equal(t, true, addParsed["url_is_secret"], "sensitive URL must signal url_is_secret=true")
	require.NotContains(t, addParsed, "url", "full URL must not be returned when url_is_secret")
	require.Contains(t, addParsed["host"], "127.0.0.1", "host field should be scheme+host of the TLS server")

	// --- 2. remote_mcp_list must not echo secret values ---
	listResult := callTool(t, th.handler, "remote_mcp_list", map[string]any{})
	listResultStr := string(listResult)
	assertNoSecretsInString(t, "remote_mcp_list response", listResultStr,
		secretURLPath, secretClientID, secretClientSec)

	var listParsed []map[string]any
	require.NoError(t, json.Unmarshal(listResult, &listParsed))
	require.Len(t, listParsed, 1)
	require.Equal(t, true, listParsed[0]["url_is_secret"])
	require.NotContains(t, listParsed[0], "url")

	// --- 3. Config file MUST contain real values (CLI dials this) ---
	cfg := generateConfigFromManager(t, th.manager)
	entry, ok := cfg.MCPServers["home-assistant"]
	require.True(t, ok)
	require.Equal(t, server.URL+secretURLPath, entry.URL, "config file must contain real URL so CLI can dial it")
	require.Equal(t, secretClientID, entry.Headers["CF-Access-Client-Id"])
	require.Equal(t, secretClientSec, entry.Headers["CF-Access-Client-Secret"])

	// --- 4. Wire test: registration ALREADY exercised the headers via the
	// initialize + tools/list calls (we require those to succeed before
	// persisting). Inspect the captured requests to prove the secrets made
	// it to the origin unchanged — same guarantee as the CLI runtime path.
	records := receivedHeaders()
	require.GreaterOrEqual(t, len(records), 2, "initialize + tools/list")
	for i, h := range records {
		require.Equal(t, secretClientID, h.Get("CF-Access-Client-Id"),
			"request %d missing CF-Access-Client-Id", i)
		require.Equal(t, secretClientSec, h.Get("CF-Access-Client-Secret"),
			"request %d missing CF-Access-Client-Secret", i)
	}

	// And an explicit CLI-equivalent hit using the config values, to prove
	// the runtime path works the same way.
	req, err := http.NewRequest(http.MethodPost, entry.URL, nil)
	require.NoError(t, err)
	for k, v := range entry.Headers {
		req.Header.Set(k, v)
	}
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	// The fake server only decodes JSON-RPC; an empty-body POST gets a
	// non-2xx status. That's fine — this test proves the request reached
	// the origin with the right headers, not that the server liked it.
	require.Less(t, resp.StatusCode, 500, "expected a client-side response, not a server error")
	final := receivedHeaders()
	require.Greater(t, len(final), len(records), "wire probe should have hit the server")
	last := final[len(final)-1]
	require.Equal(t, secretClientID, last.Get("CF-Access-Client-Id"))
	require.Equal(t, secretClientSec, last.Get("CF-Access-Client-Secret"))

	// --- 5. Error paths must not leak secret values ---
	t.Run("missing-key error does not leak secret value", func(t *testing.T) {
		th2 := setupHarness(t)
		// Don't seed the secret — register a key that won't resolve. The
		// url doesn't need to reach a server because the secret-resolution
		// failure short-circuits before ListTools is called.
		err := callToolExpectError(t, th2.handler, "remote_mcp_add", map[string]any{
			"name":                "x",
			"url":                 "https://example.com/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"header_secret_keys": map[string]string{
				"X-Auth": secretSecretKey,
			},
		})
		// Error references the key name but not any secret value that could
		// have existed under it.
		require.Contains(t, err.Error(), secretSecretKey)
		require.NotContains(t, err.Error(), secretClientSec)
	})

	t.Run("tool response never contains secret key identifiers either", func(t *testing.T) {
		// The key names themselves aren't secret (they're chosen by the
		// agent), but verifying they're not echoed back keeps response shape
		// honest — no hidden secret-key leakage channel.
		require.NotContains(t, addResultStr, urlSecretKey)
		require.NotContains(t, addResultStr, idSecretKey)
		require.NotContains(t, addResultStr, secretSecretKey)
	})
}

// assertNoSecretsInString fails the test if any of the given secret values
// appear anywhere in the string. Used to audit tool responses for accidental
// leakage of sensitive URL paths, client IDs, or secrets.
func assertNoSecretsInString(t *testing.T, label, s string, secrets ...string) {
	t.Helper()
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		require.NotContains(t, s, sec, "%s leaked secret value %q", label, sec)
	}
}

// TestRemoteMCP_LifecycleE2E exercises the full pipe that the agent takes
// when a user adds a remote MCP, from tools/list discovery through to the
// tool-permission glob expansion that gates what the Claude CLI accepts.
// Failing this test means tools will be invisible to the agent — the exact
// class of bug #13077 reports for --allowedTools wildcards.
func TestRemoteMCP_LifecycleE2E(t *testing.T) {
	t.Run("register, persist tool names, expand glob to explicit CLI-ready identifiers", func(t *testing.T) {
		exposedTools := []string{"ha_state_get", "ha_service_call", "ha_state_list"}
		server := fakeMCPServer(t, exposedTools)
		th := setupHarness(t, withHTTPClient(server.Client()))

		// --- 1. Register the remote MCP ---
		addResp := callTool(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "home-assistant",
			"url":                 server.URL + "/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers":             map[string]string{"CF-Access-Client-Id": "id"},
		})
		var addParsed map[string]any
		require.NoError(t, json.Unmarshal(addResp, &addParsed))
		require.Equal(t, "ready", addParsed["status"])

		// --- 2. Tool names persisted on the RemoteMCP entry ---
		entry, err := th.manager.GetRemoteMCP(context.Background(), "home-assistant")
		require.NoError(t, err)
		require.NotNil(t, entry)
		require.Equal(t, exposedTools, entry.ToolNames,
			"ToolNames must be captured at registration — without them the CLI glob can't expand")

		// --- 3. Agent-restart signal fires so the CLI picks up the new allowlist ---
		require.Equal(t, 1, *th.channelChangeCount,
			"OnChannelChange must fire after successful registration — otherwise the running agent won't pick up the new tools until idle timeout")

		// --- 4. Build the fully-qualified MCP tool list the router feeds to
		// the agent. Simulates the router's MCPToolNames closure so the test
		// doesn't need to start a full router.
		qualifiedNames := buildQualifiedToolNames(t, th.manager)
		for _, tool := range exposedTools {
			require.Contains(t, qualifiedNames, "mcp__home-assistant__"+tool,
				"qualified name %q missing — expandMCPGlobs will fail to match the permission glob", tool)
		}

		// --- 5. Expand a channel permission glob `mcp__home-assistant__*`
		// against the qualified list. Must produce explicit names (no glob),
		// because CLI issue #13077 confirms wildcards fail silently for MCP.
		allowedGlob := []claudecliTool{"mcp__home-assistant__*"}
		expanded := expandMCPGlobsLikeAgent(allowedGlob, qualifiedNames)
		require.Len(t, expanded, len(exposedTools),
			"glob should expand to one explicit tool name per exposed tool")
		for _, name := range expanded {
			require.NotContains(t, string(name), "*",
				"expanded entry %q still contains a wildcard — CLI will reject this", name)
			require.Contains(t, string(name), "mcp__home-assistant__",
				"expanded entry %q isn't on the home-assistant server", name)
		}

		// --- 6. Remove the MCP and confirm everything unwinds. ---
		_ = callTool(t, th.handler, "remote_mcp_remove", map[string]any{
			"name": "home-assistant",
		})
		require.Equal(t, 2, *th.channelChangeCount, "remove should also trigger agent restart")

		mcps, err := th.manager.ListRemoteMCPs(context.Background())
		require.NoError(t, err)
		require.Empty(t, mcps, "remote MCP should be gone after remove")

		qualifiedAfterRemove := buildQualifiedToolNames(t, th.manager)
		for _, n := range qualifiedAfterRemove {
			require.NotContains(t, n, "home-assistant",
				"no home-assistant tool names should remain after remove")
		}
	})

	t.Run("registration fails atomically when the MCP server is unreachable", func(t *testing.T) {
		// Server that doesn't speak MCP at all.
		server := fakeMCPServer(t, []string{"ignored"})
		server.Close() // immediately close so tools/list fails

		th := setupHarness(t, withHTTPClient(server.Client()))

		err := callToolExpectError(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "dead-server",
			"url":                 server.URL + "/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers":             map[string]string{"X-Auth": "anything"},
		})
		require.Contains(t, err.Error(), "failed to list tools")

		// Store must be clean — a broken registration is worse than no
		// registration because tools/list won't retry and the agent would
		// see a phantom server with zero tools.
		mcps, listErr := th.manager.ListRemoteMCPs(context.Background())
		require.NoError(t, listErr)
		require.Empty(t, mcps, "failed registration must not leave state behind")

		require.Equal(t, 0, *th.channelChangeCount,
			"failed registration must not trigger agent restart")
	})

	t.Run("registration fails when the MCP server exposes zero tools", func(t *testing.T) {
		server := fakeMCPServer(t, []string{}) // valid protocol, but no tools
		th := setupHarness(t, withHTTPClient(server.Client()))

		err := callToolExpectError(t, th.handler, "remote_mcp_add", map[string]any{
			"name":                "empty",
			"url":                 server.URL + "/mcp",
			"channel":             "desktop",
			"skip_auth_discovery": true,
			"headers":             map[string]string{"X-Auth": "anything"},
		})
		require.Contains(t, err.Error(), "exposed no tools")
	})
}

// buildQualifiedToolNames mirrors router.go's MCPToolNames closure so tests
// exercise the exact contract the agent's glob expansion relies on. If this
// helper's logic drifts from the router's, the tests stop protecting
// against the real bug.
func buildQualifiedToolNames(t *testing.T, mgr *remotemcpstore.Manager) []string {
	t.Helper()
	// This test doesn't exercise the local tclaw tools — those are covered
	// by TestExpandMCPGlobs. We focus on the remote-MCP contribution here.
	mcps, err := mgr.ListRemoteMCPs(context.Background())
	require.NoError(t, err)
	var names []string
	for _, m := range mcps {
		for _, tool := range m.ToolNames {
			names = append(names, "mcp__"+m.Name+"__"+tool)
		}
	}
	return names
}

// claudecliTool and expandMCPGlobsLikeAgent replicate the agent-side glob
// expander so the integration test doesn't have to cross the agent package
// boundary. The real implementation is internal/agent/agent.go
// `expandMCPGlobs` — both MUST stay in lockstep; drift means the CLI bug
// resurfaces silently.
type claudecliTool string

func expandMCPGlobsLikeAgent(tools []claudecliTool, mcpToolNames []string) []claudecliTool {
	if len(mcpToolNames) == 0 {
		return tools
	}
	var out []claudecliTool
	for _, t := range tools {
		ts := string(t)
		if !containsAny(ts, "*?[") {
			out = append(out, t)
			continue
		}
		matched := false
		for _, q := range mcpToolNames {
			if globMatch(ts, q) {
				out = append(out, claudecliTool(q))
				matched = true
			}
		}
		if !matched {
			out = append(out, t)
		}
	}
	return out
}

func containsAny(s, chars string) bool { return strings.ContainsAny(s, chars) }

// globMatch uses filepath.Match semantics — same rules the real agent uses
// (see internal/agent/agent.go).
func globMatch(pattern, name string) bool {
	ok, err := filepath.Match(pattern, name)
	return err == nil && ok
}

// --- helpers ---

// generateConfigFromManager replicates the router's buildRemoteMCPEntries
// closure inline so the test doesn't need to start a full router. If the
// router's logic diverges, these tests must be kept in sync.
func generateConfigFromManager(t *testing.T, mgr *remotemcpstore.Manager) mcp.ConfigFile {
	t.Helper()
	ctx := context.Background()

	mcps, err := mgr.ListRemoteMCPs(ctx)
	require.NoError(t, err)

	var entries []mcp.RemoteMCPEntry
	for _, m := range mcps {
		entry := mcp.RemoteMCPEntry{Name: m.Name, URL: m.URL}
		auth, err := mgr.GetRemoteMCPAuth(ctx, m.Name)
		require.NoError(t, err)
		if auth != nil {
			if auth.AccessToken != "" {
				entry.BearerToken = auth.AccessToken
			}
			if len(auth.StaticHeaders) > 0 {
				entry.ExtraHeaders = auth.StaticHeaders
			}
		}
		entries = append(entries, entry)
	}

	dir := t.TempDir()
	path, err := mcp.GenerateConfigFile(dir, "127.0.0.1:9999", "local-tok", entries)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "mcp-config.json"), path)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var cfg mcp.ConfigFile
	require.NoError(t, json.Unmarshal(raw, &cfg))
	return cfg
}
