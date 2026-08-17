package remotemcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/mcp/discovery"
)

func TestParseCallbackCode(t *testing.T) {
	const state = "the-expected-state"

	t.Run("extracts the code from a full callback URL", func(t *testing.T) {
		code, err := parseCallbackCode(
			"http://localhost:47821/callback?code=abc123&state="+state, state)
		require.NoError(t, err)
		require.Equal(t, "abc123", code)
	})

	t.Run("tolerates surrounding whitespace from a chat paste", func(t *testing.T) {
		code, err := parseCallbackCode(
			"  http://localhost:47821/callback?code=abc123&state="+state+"\n", state)
		require.NoError(t, err)
		require.Equal(t, "abc123", code)
	})

	t.Run("accepts a bare query string", func(t *testing.T) {
		code, err := parseCallbackCode("?code=abc123&state="+state, state)
		require.NoError(t, err)
		require.Equal(t, "abc123", code)
	})

	t.Run("accepts a query string with no leading question mark", func(t *testing.T) {
		code, err := parseCallbackCode("code=abc123&state="+state, state)
		require.NoError(t, err)
		require.Equal(t, "abc123", code)
	})

	t.Run("rejects a mismatched state", func(t *testing.T) {
		_, err := parseCallbackCode(
			"http://localhost:47821/callback?code=abc123&state=someone-elses", state)
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not match")
	})

	t.Run("rejects a missing state", func(t *testing.T) {
		_, err := parseCallbackCode("http://localhost:47821/callback?code=abc123", state)
		require.Error(t, err)
		require.Contains(t, err.Error(), "state")
	})

	t.Run("rejects a missing code", func(t *testing.T) {
		_, err := parseCallbackCode("http://localhost:47821/callback?state="+state, state)
		require.Error(t, err)
		require.Contains(t, err.Error(), "code")
	})

	t.Run("rejects an empty paste", func(t *testing.T) {
		_, err := parseCallbackCode("   ", state)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty")
	})

	t.Run("surfaces an OAuth error redirect", func(t *testing.T) {
		_, err := parseCallbackCode(
			"http://localhost:47821/callback?error=access_denied&error_description=User+said+no", state)
		require.Error(t, err)
		require.Contains(t, err.Error(), "access_denied")
		require.Contains(t, err.Error(), "User said no")
	})

	t.Run("reports an error redirect even when a code is present", func(t *testing.T) {
		// error takes precedence: a response carrying both is not a success.
		_, err := parseCallbackCode(
			"http://localhost:47821/callback?error=invalid_scope&code=abc&state="+state, state)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid_scope")
	})
}

func TestChooseRedirectURI(t *testing.T) {
	meta := func(authEndpoint string) *discovery.AuthMetadata {
		return &discovery.AuthMetadata{AuthorizationEndpoint: authEndpoint}
	}
	reg := &discovery.ClientRegistration{ClientID: "cid"}

	t.Run("keeps the hosted callback when the server accepts it", func(t *testing.T) {
		server := authorizeServer(t, func(redirectURI string) int { return http.StatusFound })

		got, manual, err := chooseRedirectURI(context.Background(), chooseRedirectParams{
			authMeta: meta(server.URL + "/authorize"), reg: reg,
			mcpURL: "https://mcp.example.com/mcp", hostedURL: "https://tclaw.example.com/oauth/callback",
			opts: []discovery.DiscoverAuthOption{discovery.WithDiscoverAuthHTTPClient(server.Client())},
		})

		require.NoError(t, err)
		require.False(t, manual)
		require.Equal(t, "https://tclaw.example.com/oauth/callback", got)
	})

	t.Run("falls back to loopback when only loopback is accepted", func(t *testing.T) {
		// Mirrors Strava: the hosted HTTPS callback is refused at the
		// authorization endpoint, a loopback address is allowed.
		server := authorizeServer(t, func(redirectURI string) int {
			if isLoopbackRedirect(redirectURI) {
				return http.StatusFound
			}
			return http.StatusBadRequest
		})

		got, manual, err := chooseRedirectURI(context.Background(), chooseRedirectParams{
			authMeta: meta(server.URL + "/authorize"), reg: reg,
			mcpURL: "https://mcp.example.com/mcp", hostedURL: "https://tclaw.example.com/oauth/callback",
			opts: []discovery.DiscoverAuthOption{discovery.WithDiscoverAuthHTTPClient(server.Client())},
		})

		require.NoError(t, err)
		require.True(t, manual)
		require.Equal(t, manualRedirectURI, got)
	})

	t.Run("errors when neither redirect is accepted", func(t *testing.T) {
		server := authorizeServer(t, func(string) int { return http.StatusBadRequest })

		_, _, err := chooseRedirectURI(context.Background(), chooseRedirectParams{
			authMeta: meta(server.URL + "/authorize"), reg: reg,
			mcpURL: "https://mcp.example.com/mcp", hostedURL: "https://tclaw.example.com/oauth/callback",
			opts: []discovery.DiscoverAuthOption{discovery.WithDiscoverAuthHTTPClient(server.Client())},
		})

		require.Error(t, err)
		require.Contains(t, err.Error(), "rejected both")
	})

	t.Run("forceManual skips probing entirely", func(t *testing.T) {
		var probed int
		server := authorizeServer(t, func(string) int { probed++; return http.StatusFound })

		got, manual, err := chooseRedirectURI(context.Background(), chooseRedirectParams{
			authMeta: meta(server.URL + "/authorize"), reg: reg,
			mcpURL: "https://mcp.example.com/mcp", hostedURL: "https://tclaw.example.com/oauth/callback",
			forceManual: true,
			opts:        []discovery.DiscoverAuthOption{discovery.WithDiscoverAuthHTTPClient(server.Client())},
		})

		require.NoError(t, err)
		require.True(t, manual)
		require.Equal(t, manualRedirectURI, got)
		require.Zero(t, probed, "forceManual must not touch the network")
	})

	t.Run("keeps the hosted callback when the probe cannot reach the server", func(t *testing.T) {
		// A transient network failure must not silently downgrade a working
		// server to a manual paste.
		server := authorizeServer(t, func(string) int { return http.StatusFound })
		server.Close()

		got, manual, err := chooseRedirectURI(context.Background(), chooseRedirectParams{
			authMeta: meta(server.URL + "/authorize"), reg: reg,
			mcpURL: "https://mcp.example.com/mcp", hostedURL: "https://tclaw.example.com/oauth/callback",
			opts: []discovery.DiscoverAuthOption{discovery.WithDiscoverAuthHTTPClient(server.Client())},
		})

		require.NoError(t, err)
		require.False(t, manual)
		require.Equal(t, "https://tclaw.example.com/oauth/callback", got)
	})
}

func TestGenerateState(t *testing.T) {
	t.Run("produces distinct, non-trivial tokens", func(t *testing.T) {
		seen := map[string]bool{}
		for range 100 {
			state, err := generateState()
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(state), 32)
			require.False(t, seen[state], "state tokens must not repeat")
			seen[state] = true
		}
	})
}

// --- helpers ---

// authorizeServer stands in for an OAuth authorization endpoint, deciding the
// status from the redirect_uri it was given.
func authorizeServer(t *testing.T, statusFor func(redirectURI string) int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := statusFor(r.URL.Query().Get("redirect_uri"))
		if status == http.StatusFound {
			w.Header().Set("Location", "/login")
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server
}

func isLoopbackRedirect(redirectURI string) bool {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
