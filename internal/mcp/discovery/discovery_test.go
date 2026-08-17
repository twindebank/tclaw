package discovery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildWellKnownURL(t *testing.T) {
	tests := []struct {
		name      string
		authURL   string
		expected  string
		expectErr bool
	}{
		{
			name:     "origin-only issuer",
			authURL:  "https://auth.example.com",
			expected: "https://auth.example.com/.well-known/oauth-authorization-server",
		},
		{
			// RFC 8414 §3.1 inserts the well-known segment between host and
			// the issuer path. Strava's issuer is https://www.strava.com/mcp-issuer;
			// appending to the origin instead yields a 404 and breaks discovery.
			name:     "issuer with a path component",
			authURL:  "https://www.strava.com/mcp-issuer",
			expected: "https://www.strava.com/.well-known/oauth-authorization-server/mcp-issuer",
		},
		{
			name:     "issuer path with trailing slash",
			authURL:  "https://www.strava.com/mcp-issuer/",
			expected: "https://www.strava.com/.well-known/oauth-authorization-server/mcp-issuer",
		},
		{
			name:     "multi-segment issuer path",
			authURL:  "https://id.example.com/tenants/acme",
			expected: "https://id.example.com/.well-known/oauth-authorization-server/tenants/acme",
		},
		{
			name:     "root path is treated as no path",
			authURL:  "https://auth.example.com/",
			expected: "https://auth.example.com/.well-known/oauth-authorization-server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildWellKnownURL(tt.authURL)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestBuildAuthURLWithPKCE(t *testing.T) {
	meta := &AuthMetadata{
		AuthorizationEndpoint: "https://auth.example.com/authorize",
		ScopesSupported:       []string{"read", "activity:read_all"},
	}
	reg := &ClientRegistration{ClientID: "client-123"}

	t.Run("includes scopes from resource metadata", func(t *testing.T) {
		authURL, _ := BuildAuthURLWithPKCE(AuthURLParams{
			Meta: meta, Reg: reg, State: "st", RedirectURI: "https://app/cb", MCPURL: "https://mcp/x",
		})

		parsed, err := url.Parse(authURL)
		require.NoError(t, err)
		q := parsed.Query()
		require.Equal(t, "read activity:read_all", q.Get("scope"))
		require.Equal(t, "client-123", q.Get("client_id"))
		require.Equal(t, "https://app/cb", q.Get("redirect_uri"))
		require.Equal(t, "https://mcp/x", q.Get("resource"))
		require.Equal(t, "S256", q.Get("code_challenge_method"))
		require.NotEmpty(t, q.Get("code_challenge"))
	})

	t.Run("omits scope when the resource declares none", func(t *testing.T) {
		bare := &AuthMetadata{AuthorizationEndpoint: "https://auth.example.com/authorize"}
		authURL, _ := BuildAuthURLWithPKCE(AuthURLParams{
			Meta: bare, Reg: reg, State: "st", RedirectURI: "https://app/cb", MCPURL: "https://mcp/x",
		})

		parsed, err := url.Parse(authURL)
		require.NoError(t, err)
		_, present := parsed.Query()["scope"]
		require.False(t, present, "scope must be absent, not empty")
	})

	t.Run("reusing a verifier keeps the challenge stable", func(t *testing.T) {
		// Rebuilding the URL to swap in the real state token must not mint a
		// fresh verifier: the challenge the user carries to the auth server
		// has to match the verifier the token exchange later presents.
		first, verifier := BuildAuthURLWithPKCE(AuthURLParams{
			Meta: meta, Reg: reg, RedirectURI: "https://app/cb", MCPURL: "https://mcp/x",
		})
		second, reused := BuildAuthURLWithPKCE(AuthURLParams{
			Meta: meta, Reg: reg, State: "real-state", RedirectURI: "https://app/cb",
			MCPURL: "https://mcp/x", CodeVerifier: verifier,
		})

		require.Equal(t, verifier, reused)
		require.Equal(t, challengeOf(t, first), challengeOf(t, second))
		require.Equal(t, "real-state", queryOf(t, second).Get("state"))
	})

	t.Run("without a reused verifier each build differs", func(t *testing.T) {
		first, v1 := BuildAuthURLWithPKCE(AuthURLParams{
			Meta: meta, Reg: reg, RedirectURI: "https://app/cb", MCPURL: "https://mcp/x",
		})
		second, v2 := BuildAuthURLWithPKCE(AuthURLParams{
			Meta: meta, Reg: reg, RedirectURI: "https://app/cb", MCPURL: "https://mcp/x",
		})

		require.NotEqual(t, v1, v2)
		require.NotEqual(t, challengeOf(t, first), challengeOf(t, second))
	})
}

func TestRegisterClient(t *testing.T) {
	t.Run("sends client_name and requests JSON", func(t *testing.T) {
		var gotBody clientRegistrationRequest
		var gotAccept string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAccept = r.Header.Get("Accept")
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(body, &gotBody))
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "abc"})
		}))
		t.Cleanup(server.Close)

		reg, err := RegisterClient(context.Background(), RegisterClientParams{
			Meta:        &AuthMetadata{RegistrationEndpoint: server.URL},
			RedirectURI: "https://app/cb",
			ClientName:  "Claude Code",
		}, WithDiscoverAuthHTTPClient(server.Client()))
		require.NoError(t, err)
		require.Equal(t, "abc", reg.ClientID)
		require.Equal(t, "Claude Code", gotBody.ClientName)
		require.Equal(t, []string{"https://app/cb"}, gotBody.RedirectURIs)
		require.Contains(t, gotAccept, "application/json")
	})

	t.Run("surfaces the server's rejection body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_client_metadata"}`))
		}))
		t.Cleanup(server.Close)

		_, err := RegisterClient(context.Background(), RegisterClientParams{
			Meta:        &AuthMetadata{RegistrationEndpoint: server.URL},
			RedirectURI: "https://app/cb",
		}, WithDiscoverAuthHTTPClient(server.Client()))
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid_client_metadata")
	})

	t.Run("rejects a server with no registration endpoint", func(t *testing.T) {
		_, err := RegisterClient(context.Background(), RegisterClientParams{
			Meta: &AuthMetadata{}, RedirectURI: "https://app/cb",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "dynamic client registration")
	})
}

func TestRedirectURIAccepted(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		accepted bool
		wantErr  bool
	}{
		{name: "302 to a login page means accepted", status: http.StatusFound, accepted: true},
		{name: "200 consent page means accepted", status: http.StatusOK, accepted: true},
		{name: "400 means the redirect uri was refused", status: http.StatusBadRequest},
		{name: "401 means refused", status: http.StatusUnauthorized},
		{name: "500 is not a verdict", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.status == http.StatusFound {
					w.Header().Set("Location", "https://elsewhere.example.com/login")
				}
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(server.Close)

			accepted, err := RedirectURIAccepted(context.Background(), server.URL+"/authorize?x=1",
				WithDiscoverAuthHTTPClient(server.Client()))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.accepted, accepted)
		})
	}

	t.Run("does not follow the redirect", func(t *testing.T) {
		var hits int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits++
			if strings.HasPrefix(r.URL.Path, "/authorize") {
				w.Header().Set("Location", "/login")
				w.WriteHeader(http.StatusFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(server.Close)

		accepted, err := RedirectURIAccepted(context.Background(), server.URL+"/authorize",
			WithDiscoverAuthHTTPClient(server.Client()))
		require.NoError(t, err)
		require.True(t, accepted)
		require.Equal(t, 1, hits, "the login page must not be fetched")
	})
}

func TestDiscoverAuth_ScopesFromResourceMetadata(t *testing.T) {
	t.Run("carries scopes_supported through to auth metadata", func(t *testing.T) {
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/mcp":
				w.Header().Set("WWW-Authenticate",
					`Bearer resource_metadata="`+server.URL+`/.well-known/oauth-protected-resource"`)
				w.WriteHeader(http.StatusUnauthorized)
			case "/.well-known/oauth-protected-resource":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"resource":              server.URL + "/mcp",
					"authorization_servers": []string{server.URL + "/issuer-path"},
					"scopes_supported":      []string{"read", "activity:read_all"},
				})
			case "/.well-known/oauth-authorization-server/issuer-path":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"issuer":                 server.URL,
					"authorization_endpoint": server.URL + "/authorize",
					"token_endpoint":         server.URL + "/token",
					"registration_endpoint":  server.URL + "/register",
				})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		t.Cleanup(server.Close)

		meta, err := DiscoverAuth(context.Background(), server.URL+"/mcp",
			WithDiscoverAuthHTTPClient(server.Client()))
		require.NoError(t, err)
		require.NotNil(t, meta)
		require.Equal(t, []string{"read", "activity:read_all"}, meta.ScopesSupported)
		require.Equal(t, server.URL+"/authorize", meta.AuthorizationEndpoint)
		require.Equal(t, server.URL+"/register", meta.RegistrationEndpoint)
	})
}

// --- helpers ---

func queryOf(t *testing.T, rawURL string) url.Values {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)
	return parsed.Query()
}

func challengeOf(t *testing.T, rawURL string) string {
	t.Helper()
	return queryOf(t, rawURL).Get("code_challenge")
}
