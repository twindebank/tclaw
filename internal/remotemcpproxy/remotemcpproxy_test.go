package remotemcpproxy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp/discovery"
	"tclaw/internal/remotemcpproxy"
	"tclaw/internal/remotemcpstore"
)

func TestServer_ForwardsAndInjectsAuth(t *testing.T) {
	t.Run("injects bearer token upstream and never exposes it to the caller", func(t *testing.T) {
		upstream, rec := newUpstream(t, "upstream-body-ok")
		mgr := newManager(t)
		register(t, mgr, "linear", upstream.URL+"/mcp", &remotemcpstore.RemoteMCPAuth{AccessToken: "s3cret-access"})

		srv := startProxy(t, mgr, nil)
		resp, body := proxyGET(t, srv, "linear")

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "upstream-body-ok", body)
		// Upstream received the injected Authorization; the caller-facing bytes did not.
		require.Equal(t, "Bearer s3cret-access", rec.lastHeader().Get("Authorization"))
		require.NotContains(t, body, "s3cret-access")
	})

	t.Run("injects static headers upstream", func(t *testing.T) {
		upstream, rec := newUpstream(t, "ok")
		mgr := newManager(t)
		register(t, mgr, "ha", upstream.URL+"/mcp", &remotemcpstore.RemoteMCPAuth{
			StaticHeaders: map[string]string{
				"CF-Access-Client-Id":     "client-id",
				"CF-Access-Client-Secret": "client-secret",
			},
		})

		srv := startProxy(t, mgr, nil)
		resp, _ := proxyGET(t, srv, "ha")

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "client-id", rec.lastHeader().Get("CF-Access-Client-Id"))
		require.Equal(t, "client-secret", rec.lastHeader().Get("CF-Access-Client-Secret"))
	})

	t.Run("forwards to a url_secret_key upstream and strips the proxy token", func(t *testing.T) {
		upstream, rec := newUpstream(t, "ok")
		mgr := newManager(t)
		// The credential lives in the URL path/query; no stored auth headers.
		register(t, mgr, "sensitive", upstream.URL+"/private_path?token=url-secret", nil)

		srv := startProxy(t, mgr, nil)
		resp, _ := proxyGET(t, srv, "sensitive")

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "/private_path", rec.lastPath())
		require.Equal(t, "url-secret", rec.lastQuery().Get("token"))
		// The proxy-hop token must never leak to the upstream.
		require.Empty(t, rec.lastHeader().Get(remotemcpproxy.ProxyTokenHeader))
	})

	t.Run("forwards request body and returns the upstream response", func(t *testing.T) {
		upstream, rec := newUpstream(t, "echoed")
		mgr := newManager(t)
		register(t, mgr, "srv1", upstream.URL+"/mcp", nil)

		srv := startProxy(t, mgr, nil)
		req, err := http.NewRequest(http.MethodPost, srv.RemoteURL("srv1"), strings.NewReader(`{"jsonrpc":"2.0"}`))
		require.NoError(t, err)
		req.Header.Set(remotemcpproxy.ProxyTokenHeader, srv.Token())
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, `{"jsonrpc":"2.0"}`, rec.lastBody())
	})

	t.Run("streams server-sent events through without buffering", func(t *testing.T) {
		mgr := newManager(t)
		sse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			for i := 0; i < 3; i++ {
				_, _ = io.WriteString(w, "data: event\n\n")
				flusher.Flush()
			}
		}))
		t.Cleanup(sse.Close)
		register(t, mgr, "streamer", sse.URL+"/sse", nil)

		srv := startProxy(t, mgr, nil)
		resp, body := proxyGET(t, srv, "streamer")

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
		require.Equal(t, 3, strings.Count(body, "data: event"))
	})
}

func TestServer_RejectsUnauthorizedOrUnknown(t *testing.T) {
	t.Run("missing proxy token is forbidden", func(t *testing.T) {
		upstream, _ := newUpstream(t, "ok")
		mgr := newManager(t)
		register(t, mgr, "linear", upstream.URL+"/mcp", nil)
		srv := startProxy(t, mgr, nil)

		resp, err := http.Get(srv.RemoteURL("linear"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("wrong proxy token is forbidden", func(t *testing.T) {
		upstream, _ := newUpstream(t, "ok")
		mgr := newManager(t)
		register(t, mgr, "linear", upstream.URL+"/mcp", nil)
		srv := startProxy(t, mgr, nil)

		req, err := http.NewRequest(http.MethodGet, srv.RemoteURL("linear"), nil)
		require.NoError(t, err)
		req.Header.Set(remotemcpproxy.ProxyTokenHeader, "not-the-token")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("unknown server name is forbidden, not an open relay", func(t *testing.T) {
		mgr := newManager(t)
		srv := startProxy(t, mgr, nil)

		req, err := http.NewRequest(http.MethodGet, srv.RemoteURL("does-not-exist"), nil)
		require.NoError(t, err)
		req.Header.Set(remotemcpproxy.ProxyTokenHeader, srv.Token())
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("root path with no server name is forbidden", func(t *testing.T) {
		mgr := newManager(t)
		srv := startProxy(t, mgr, nil)

		req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/", nil)
		require.NoError(t, err)
		req.Header.Set(remotemcpproxy.ProxyTokenHeader, srv.Token())
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}

func TestServer_RefreshesExpiredToken(t *testing.T) {
	t.Run("refreshes an expired token, injects the new one, and persists it", func(t *testing.T) {
		upstream, rec := newUpstream(t, "ok")
		mgr := newManager(t)
		register(t, mgr, "oauthy", upstream.URL+"/mcp", &remotemcpstore.RemoteMCPAuth{
			AccessToken:  "stale",
			RefreshToken: "refresh-me",
			TokenExpiry:  time.Now().Add(-time.Hour),
		})

		var refreshCalls int
		refresher := func(_ context.Context, auth *remotemcpstore.RemoteMCPAuth, _ string) (*discovery.RemoteCredentials, error) {
			refreshCalls++
			require.Equal(t, "refresh-me", auth.RefreshToken)
			return &discovery.RemoteCredentials{AccessToken: "fresh", RefreshToken: "next-refresh", ExpiresIn: 3600}, nil
		}

		srv := startProxy(t, mgr, refresher)
		resp, _ := proxyGET(t, srv, "oauthy")

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, 1, refreshCalls)
		require.Equal(t, "Bearer fresh", rec.lastHeader().Get("Authorization"))

		// The refreshed token is persisted with a future expiry.
		stored, err := mgr.GetRemoteMCPAuth(context.Background(), "oauthy")
		require.NoError(t, err)
		require.Equal(t, "fresh", stored.AccessToken)
		require.Equal(t, "next-refresh", stored.RefreshToken)
		require.False(t, stored.TokenExpired())
	})

	t.Run("does not refresh a valid token", func(t *testing.T) {
		upstream, rec := newUpstream(t, "ok")
		mgr := newManager(t)
		register(t, mgr, "fresh-already", upstream.URL+"/mcp", &remotemcpstore.RemoteMCPAuth{
			AccessToken:  "still-good",
			RefreshToken: "unused",
			TokenExpiry:  time.Now().Add(time.Hour),
		})

		refresher := func(context.Context, *remotemcpstore.RemoteMCPAuth, string) (*discovery.RemoteCredentials, error) {
			t.Fatal("refresher must not be called for a valid token")
			return nil, nil
		}

		srv := startProxy(t, mgr, refresher)
		resp, _ := proxyGET(t, srv, "fresh-already")

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "Bearer still-good", rec.lastHeader().Get("Authorization"))
	})
}

// --- helpers ---

func newManager(t *testing.T) *remotemcpstore.Manager {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return remotemcpstore.NewManager(s, newMemorySecretStore())
}

func register(t *testing.T, mgr *remotemcpstore.Manager, name, url string, auth *remotemcpstore.RemoteMCPAuth) {
	t.Helper()
	_, err := mgr.AddRemoteMCP(context.Background(), remotemcpstore.AddRemoteMCPParams{
		Name:      name,
		URL:       url,
		Channel:   "desktop",
		ToolNames: []string{"probe"},
	})
	require.NoError(t, err)
	if auth != nil {
		require.NoError(t, mgr.SetRemoteMCPAuth(context.Background(), name, auth))
	}
}

func startProxy(t *testing.T, mgr *remotemcpstore.Manager, refresher remotemcpproxy.TokenRefresher) *remotemcpproxy.Server {
	t.Helper()
	srv, err := remotemcpproxy.NewServer(remotemcpproxy.Config{Store: mgr, Refresher: refresher})
	require.NoError(t, err)
	_, err = srv.Start("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	return srv
}

func proxyGET(t *testing.T, srv *remotemcpproxy.Server, name string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.RemoteURL(name), nil)
	require.NoError(t, err)
	req.Header.Set(remotemcpproxy.ProxyTokenHeader, srv.Token())
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(body)
}

// recorder captures what the fake upstream received on each request.
type recorder struct {
	mu      sync.Mutex
	headers []http.Header
	paths   []string
	queries []url.Values
	bodies  []string
}

func (r *recorder) record(req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.headers = append(r.headers, req.Header.Clone())
	r.paths = append(r.paths, req.URL.Path)
	r.queries = append(r.queries, req.URL.Query())
	r.bodies = append(r.bodies, string(body))
}

func (r *recorder) lastHeader() http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.headers[len(r.headers)-1]
}

func (r *recorder) lastPath() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paths[len(r.paths)-1]
}

func (r *recorder) lastQuery() url.Values {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queries[len(r.queries)-1]
}

func (r *recorder) lastBody() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bodies[len(r.bodies)-1]
}

func newUpstream(t *testing.T, responseBody string) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rec.record(req)
		_, _ = io.WriteString(w, responseBody)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// memorySecretStore is a map-backed secret.Store for tests.
type memorySecretStore struct {
	mu   sync.Mutex
	data map[string]string
}

func newMemorySecretStore() *memorySecretStore {
	return &memorySecretStore{data: make(map[string]string)}
}

func (m *memorySecretStore) Get(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data[key], nil
}

func (m *memorySecretStore) Set(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *memorySecretStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}
