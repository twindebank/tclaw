package remotemcpproxy_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
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

func TestServer_PinnedTLSUpstream(t *testing.T) {
	t.Run("forwards to a self-signed https upstream whose cert matches the pin", func(t *testing.T) {
		upstream, rec := newTLSUpstream(t, "pinned-ok")
		mgr := newManager(t)
		registerPinned(t, mgr, "pinned-mcp", upstream.URL+"/mcp", certPin(t, upstream))

		srv := startProxy(t, mgr, nil)
		resp, body := proxyGET(t, srv, "pinned-mcp")

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "pinned-ok", body)
		require.Equal(t, "/mcp", rec.lastPath())
	})

	t.Run("refuses a self-signed https upstream whose cert does not match the pin", func(t *testing.T) {
		upstream, _ := newTLSUpstream(t, "should-not-reach")
		mgr := newManager(t)
		// A valid-shape pin that belongs to a different certificate.
		wrongPin := strings.Repeat("ab", 32)
		registerPinned(t, mgr, "pinned-mcp", upstream.URL+"/mcp", wrongPin)

		srv := startProxy(t, mgr, nil)
		resp, body := proxyGET(t, srv, "pinned-mcp")

		// The TLS pin mismatch fails the dial, so the proxy's ErrorHandler returns 502.
		require.Equal(t, http.StatusBadGateway, resp.StatusCode)
		require.NotContains(t, body, "should-not-reach")
	})
}

func TestServer_ColdStartRetry(t *testing.T) {
	t.Run("retries a sleeping upstream that resets the connection while it wakes", func(t *testing.T) {
		// The upstream refuses (resets) its first two connections — the way a Fly
		// autostop machine behaves mid-cold-start — then serves normally.
		upstreamURL, rec := flakyUpstream(t, 2, "awake-now")
		mgr := newManager(t)
		register(t, mgr, "understudy", upstreamURL+"/mcp", &remotemcpstore.RemoteMCPAuth{AccessToken: "tok"})

		srv := startProxy(t, mgr, nil)
		resp, body := proxyPOST(t, srv, "understudy", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "awake-now", body)
		// The buffered request body was replayed intact on the winning attempt.
		require.Equal(t, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, rec.lastBody())
		require.Equal(t, "Bearer tok", rec.lastHeader().Get("Authorization"))
	})

	t.Run("gives up and 502s when the upstream never comes up", func(t *testing.T) {
		// More failing connections than the retry budget: the proxy must surface a
		// 502 rather than hang or retry forever.
		upstreamURL, _ := flakyUpstream(t, 50, "never-reached")
		mgr := newManager(t)
		register(t, mgr, "understudy", upstreamURL+"/mcp", nil)

		srv := startProxy(t, mgr, nil)
		resp, _ := proxyGET(t, srv, "understudy")

		require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	})

	t.Run("does not retry a real HTTP error response", func(t *testing.T) {
		// A 500 from a live upstream is a genuine response, not a cold start — it
		// must pass straight through on the first attempt.
		var attempts atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(upstream.Close)

		mgr := newManager(t)
		register(t, mgr, "understudy", upstream.URL+"/mcp", nil)

		srv := startProxy(t, mgr, nil)
		resp, _ := proxyGET(t, srv, "understudy")

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		require.Equal(t, int32(1), attempts.Load(), "HTTP errors must not be retried")
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
	// A tiny retry budget: the production one waits out a ~30s browser cold start,
	// which a test exercising the give-up path would otherwise sit through.
	srv, err := remotemcpproxy.NewServer(remotemcpproxy.Config{
		Store:          mgr,
		Refresher:      refresher,
		ColdStartRetry: discovery.ColdStartRetry{MaxAttempts: 4, BackoffCap: 10 * time.Millisecond},
	})
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

// newTLSUpstream is like newUpstream but serves HTTPS with a throwaway
// self-signed cert (httptest's), so we can exercise the proxy's cert pinning.
func newTLSUpstream(t *testing.T, responseBody string) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rec.record(req)
		_, _ = io.WriteString(w, responseBody)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// certPin returns the hex SHA-256 fingerprint of a TLS server's leaf cert — the
// value a client would pin.
func certPin(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	require.NotNil(t, srv.Certificate())
	sum := sha256.Sum256(srv.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

func registerPinned(t *testing.T, mgr *remotemcpstore.Manager, name, url, pin string) {
	t.Helper()
	_, err := mgr.AddRemoteMCP(context.Background(), remotemcpstore.AddRemoteMCPParams{
		Name:         name,
		URL:          url,
		Channel:      "desktop",
		ToolNames:    []string{"probe"},
		TLSPinSHA256: pin,
	})
	require.NoError(t, err)
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

func proxyPOST(t *testing.T, srv *remotemcpproxy.Server, name, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.RemoteURL(name), strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set(remotemcpproxy.ProxyTokenHeader, srv.Token())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(respBody)
}

// flakyUpstream serves responseBody over plain HTTP, but resets its first
// failFirst connections before answering — reproducing a Fly autostop machine
// that resets connections while it wakes. It returns the base URL and a recorder
// of the requests that reached the handler.
func flakyUpstream(t *testing.T, failFirst int, responseBody string) (string, *recorder) {
	t.Helper()
	rec := &recorder{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rec.record(req)
		_, _ = io.WriteString(w, responseBody)
	})}
	go func() { _ = srv.Serve(&flakyListener{Listener: ln, failFirst: failFirst}) }()
	t.Cleanup(func() { _ = srv.Close() })

	return "http://" + ln.Addr().String(), rec
}

// flakyListener closes the first failFirst accepted connections before the HTTP
// server ever sees them, so the client observes a pre-response reset/EOF and the
// proxy's cold-start retry kicks in.
type flakyListener struct {
	net.Listener
	failFirst int
	count     atomic.Int32
}

func (l *flakyListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if int(l.count.Add(1)) <= l.failFirst {
			_ = conn.Close()
			continue
		}
		return conn, nil
	}
}
