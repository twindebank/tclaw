package remotemcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/remotemcpstore"
	"tclaw/internal/tool/remotemcp"
)

func TestRemoteMCPAuthComplete(t *testing.T) {
	const pendingState = "pending-state-token"

	t.Run("exchanges a pasted callback and captures the tool list", func(t *testing.T) {
		var tokenForm url.Values
		server := manualAuthServer(t, manualAuthServerOpts{
			toolNames: []string{"list_activities", "get_athlete_profile"},
			onToken:   func(form url.Values) { tokenForm = form },
		})
		h, mgr := setupManualAuth(t, server)
		seedPending(t, mgr, server, pendingState)

		result := callTool(t, h, "remote_mcp_auth_complete", map[string]any{
			"name":         "strava",
			"callback_url": "http://localhost:47821/callback?code=the-code&state=" + pendingState,
		})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "authorized", got["status"])

		// The exchange must replay the loopback redirect URI verbatim and
		// present the stored PKCE verifier — an auth server checks both.
		require.Equal(t, "the-code", tokenForm.Get("code"))
		require.Equal(t, "authorization_code", tokenForm.Get("grant_type"))
		require.Equal(t, "http://localhost:47821/callback", tokenForm.Get("redirect_uri"))
		require.Equal(t, "stored-verifier", tokenForm.Get("code_verifier"))

		auth, err := mgr.GetRemoteMCPAuth(context.Background(), "strava")
		require.NoError(t, err)
		require.Equal(t, "issued-access-token", auth.AccessToken)
		require.Equal(t, "issued-refresh-token", auth.RefreshToken)
		require.False(t, auth.TokenExpiry.IsZero(), "expiry must be recorded so the proxy can refresh")
		require.Nil(t, auth.PendingExchange, "the spent exchange must be cleared")

		entry, err := mgr.GetRemoteMCP(context.Background(), "strava")
		require.NoError(t, err)
		require.Equal(t, []string{"list_activities", "get_athlete_profile"}, entry.ToolNames)
	})

	t.Run("rejects a callback whose state does not match", func(t *testing.T) {
		server := manualAuthServer(t, manualAuthServerOpts{toolNames: []string{"x"}})
		h, mgr := setupManualAuth(t, server)
		seedPending(t, mgr, server, pendingState)

		err := callToolExpectError(t, h, "remote_mcp_auth_complete", map[string]any{
			"name":         "strava",
			"callback_url": "http://localhost:47821/callback?code=the-code&state=wrong",
		})
		require.Contains(t, err.Error(), "does not match")

		auth, getErr := mgr.GetRemoteMCPAuth(context.Background(), "strava")
		require.NoError(t, getErr)
		require.Empty(t, auth.AccessToken, "a rejected paste must not store a token")
		require.NotNil(t, auth.PendingExchange, "the pending exchange must survive for a retry")
	})

	t.Run("rejects an expired pending authorization", func(t *testing.T) {
		server := manualAuthServer(t, manualAuthServerOpts{toolNames: []string{"x"}})
		h, mgr := setupManualAuth(t, server)
		seedPending(t, mgr, server, pendingState)

		auth, err := mgr.GetRemoteMCPAuth(context.Background(), "strava")
		require.NoError(t, err)
		auth.PendingExchange.StartedAt = time.Now().Add(-2 * time.Hour)
		require.NoError(t, mgr.SetRemoteMCPAuth(context.Background(), "strava", auth))

		callErr := callToolExpectError(t, h, "remote_mcp_auth_complete", map[string]any{
			"name":         "strava",
			"callback_url": "http://localhost:47821/callback?code=the-code&state=" + pendingState,
		})
		require.Contains(t, callErr.Error(), "expired")
	})

	t.Run("rejects a server with no pending authorization", func(t *testing.T) {
		server := manualAuthServer(t, manualAuthServerOpts{toolNames: []string{"x"}})
		h, mgr := setupManualAuth(t, server)
		_, err := mgr.AddRemoteMCP(context.Background(), remotemcpstore.AddRemoteMCPParams{
			Name: "strava", URL: server.URL + "/mcp", Channel: "running",
		})
		require.NoError(t, err)
		require.NoError(t, mgr.SetRemoteMCPAuth(context.Background(), "strava",
			&remotemcpstore.RemoteMCPAuth{ClientID: "cid"}))

		callErr := callToolExpectError(t, h, "remote_mcp_auth_complete", map[string]any{
			"name":         "strava",
			"callback_url": "http://localhost:47821/callback?code=c&state=s",
		})
		require.Contains(t, callErr.Error(), "no manual authorization is pending")
	})

	t.Run("rejects an unknown remote MCP", func(t *testing.T) {
		server := manualAuthServer(t, manualAuthServerOpts{toolNames: []string{"x"}})
		h, _ := setupManualAuth(t, server)

		err := callToolExpectError(t, h, "remote_mcp_auth_complete", map[string]any{
			"name":         "nope",
			"callback_url": "http://localhost:47821/callback?code=c&state=s",
		})
		require.Contains(t, err.Error(), "no remote MCP named")
	})

	t.Run("surfaces a token endpoint rejection without storing anything", func(t *testing.T) {
		server := manualAuthServer(t, manualAuthServerOpts{
			toolNames:  []string{"x"},
			tokenError: `{"error":"invalid_grant"}`,
		})
		h, mgr := setupManualAuth(t, server)
		seedPending(t, mgr, server, pendingState)

		err := callToolExpectError(t, h, "remote_mcp_auth_complete", map[string]any{
			"name":         "strava",
			"callback_url": "http://localhost:47821/callback?code=stale&state=" + pendingState,
		})
		require.Contains(t, err.Error(), "invalid_grant")

		auth, getErr := mgr.GetRemoteMCPAuth(context.Background(), "strava")
		require.NoError(t, getErr)
		require.Empty(t, auth.AccessToken)
	})
}

// --- helpers ---

type manualAuthServerOpts struct {
	toolNames  []string
	onToken    func(url.Values)
	tokenError string
}

// manualAuthServer serves both the OAuth token endpoint and a minimal MCP
// server from one TLS host, so a single server.Client() trusts both — the
// tool needs to reach the token endpoint and then tools/list.
func manualAuthServer(t *testing.T, opts manualAuthServerOpts) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		if opts.onToken != nil {
			opts.onToken(r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		if opts.tokenError != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(opts.tokenError))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "issued-access-token",
			"refresh_token": "issued-refresh-token",
			"expires_in":    3600,
		})
	})

	mux.HandleFunc("/mcp", fakeMCPHandler(opts.toolNames, "", nil))

	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}

func setupManualAuth(t *testing.T, server *httptest.Server) (*mcp.Handler, *remotemcpstore.Manager) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	secrets := &memorySecretStore{data: map[string]string{}}
	mgr := remotemcpstore.NewManager(s, secrets)

	deps := remotemcp.Deps{
		Manager:       mgr,
		SecretStore:   secrets,
		HTTPClient:    server.Client(),
		ConfigUpdater: func(context.Context) error { return nil },
	}

	handler := mcp.NewHandler()
	remotemcp.RegisterTools(handler, deps)
	remotemcp.RegisterAuthWaitTool(handler, deps)
	return handler, mgr
}

// seedPending puts a remote MCP into the state remote_mcp_add leaves behind
// for a loopback flow: registered, auth metadata stored, exchange pending.
func seedPending(t *testing.T, mgr *remotemcpstore.Manager, server *httptest.Server, state string) {
	t.Helper()
	ctx := context.Background()
	_, err := mgr.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{
		Name: "strava", URL: server.URL + "/mcp", Channel: "running",
	})
	require.NoError(t, err)
	require.NoError(t, mgr.SetRemoteMCPAuth(ctx, "strava", &remotemcpstore.RemoteMCPAuth{
		AuthorizationEndpoint: server.URL + "/authorize",
		TokenEndpoint:         server.URL + "/token",
		ClientID:              "248572",
		PendingExchange: &remotemcpstore.PendingExchange{
			CodeVerifier: "stored-verifier",
			State:        state,
			RedirectURI:  "http://localhost:47821/callback",
			StartedAt:    time.Now(),
		},
	}))
}
