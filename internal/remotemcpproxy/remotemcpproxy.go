// Package remotemcpproxy runs a per-user localhost reverse proxy that fronts
// every remote MCP server a user has connected, injecting each server's
// credentials server-side so they never enter the agent sandbox.
//
// The agent's --mcp-config points each remote server at a token-free
// http://127.0.0.1:<port>/<name> URL. On each request the proxy resolves the
// real upstream URL and credentials from the encrypted store, refreshes an
// expired OAuth token on demand, injects the Authorization / static headers,
// and forwards to the upstream. Because the mcp-config directory is bind-mounted
// read-only into the sandbox, keeping credentials out of that file is the only
// way to grant the agent MCP access without disclosing the tokens — the same
// rationale as internal/knowledgeproxy for git.
//
// Requests must present the per-user proxy token in the X-Tclaw-Proxy-Token
// header (a benign localhost credential, mirroring the local tclaw MCP server)
// and target a registered server name; everything else is rejected.
package remotemcpproxy

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"tclaw/internal/egressproxy"
	"tclaw/internal/mcp/discovery"
	"tclaw/internal/remotemcpstore"
)

// ProxyTokenHeader carries the benign per-user localhost token that authorizes
// use of the proxy. Distinct from Authorization so it never collides with an
// upstream server's own auth header, which the proxy sets server-side.
const ProxyTokenHeader = "X-Tclaw-Proxy-Token"

// Store is the subset of remotemcpstore.Manager the proxy depends on.
// *remotemcpstore.Manager satisfies it.
type Store interface {
	GetRemoteMCP(ctx context.Context, name string) (*remotemcpstore.RemoteMCP, error)
	GetRemoteMCPAuth(ctx context.Context, name string) (*remotemcpstore.RemoteMCPAuth, error)
	SetRemoteMCPAuth(ctx context.Context, name string, auth *remotemcpstore.RemoteMCPAuth) error
}

// TokenRefresher exchanges a stored refresh token for new credentials. The
// default hits the OAuth token endpoint via internal/mcp/discovery; tests
// override it because the discovery client refuses non-HTTPS / loopback hosts.
type TokenRefresher func(ctx context.Context, auth *remotemcpstore.RemoteMCPAuth, mcpURL string) (*discovery.RemoteCredentials, error)

// Config configures a remote-MCP proxy server.
type Config struct {
	// Store resolves upstream URLs and credentials per request.
	Store Store

	// Refresher exchanges refresh tokens for new access tokens. Optional;
	// defaults to a discovery-backed implementation.
	Refresher TokenRefresher

	// ColdStartRetry bounds how long a request waits out an upstream waking from
	// autostop. Optional; defaults to discovery.DefaultColdStartRetry. Tests
	// shrink it so exercising the give-up path doesn't burn the real budget.
	ColdStartRetry discovery.ColdStartRetry

	// EgressProxy routes upstreams whose host it names through a CONNECT
	// proxy. Nil leaves every upstream on its direct path.
	EgressProxy *egressproxy.Proxy
}

// route carries the resolved upstream target and injected headers from the
// request handler to the reverse-proxy Director (and its TLS dialer), without
// struct-field state.
type route struct {
	// name is the registered server name, carried for logging only.
	name string

	upstream *url.URL
	subpath  string
	headers  map[string]string

	// tlsPin, when set, is the hex SHA-256 fingerprint the upstream's TLS leaf
	// cert must match — for self-signed servers on Fly private hosts.
	tlsPin string
}

type routeContextKey struct{}

// Server is a running remote-MCP proxy.
type Server struct {
	store     Store
	refresher TokenRefresher
	token     string
	proxy     *httputil.ReverseProxy

	listener net.Listener
	srv      *http.Server

	// refreshMu serializes OAuth token refreshes so concurrent requests for an
	// expired token don't each mint a new one (a store read-modify-write race).
	refreshMu sync.Mutex

	mu      sync.Mutex
	running bool
}

// NewServer builds a proxy server from cfg. It mints a random proxy token and
// does not start listening until Start is called.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("store is required")
	}

	refresher := cfg.Refresher
	if refresher == nil {
		refresher = defaultRefresher
	}

	coldStartRetry := cfg.ColdStartRetry
	if coldStartRetry.MaxAttempts == 0 {
		coldStartRetry = discovery.DefaultColdStartRetry
	}

	s := &Server{
		store:     cfg.Store,
		refresher: refresher,
		token:     generateToken(),
	}

	s.proxy = &httputil.ReverseProxy{
		// FlushInterval -1 streams responses immediately without buffering, so
		// MCP server-sent-event streams pass through the proxy in real time.
		FlushInterval: -1,
		// Custom TLS dial so a per-server cert pin (route.tlsPin) authenticates a
		// self-signed upstream by exact fingerprint instead of the system trust
		// store. Only affects https upstreams; plain-http upstreams use the
		// default DialContext untouched. Wrapped in the shared cold-start retry so
		// a sleeping autostop upstream that resets the connection while it
		// cold-starts is retried rather than surfaced as an immediate 502.
		Transport: discovery.NewColdStartRetryTransport(pinAwareTransport(cfg.EgressProxy), coldStartRetry),
		Director: func(req *http.Request) {
			rt, ok := req.Context().Value(routeContextKey{}).(*route)
			if !ok {
				// handle() always sets the route before serving; a missing value
				// means a programming error rather than a client condition.
				slog.Error("remote mcp proxy: missing route in request context")
				return
			}
			req.URL.Scheme = rt.upstream.Scheme
			req.URL.Host = rt.upstream.Host
			req.Host = rt.upstream.Host
			req.URL.Path = singleJoiningSlash(rt.upstream.Path, rt.subpath)
			req.URL.RawQuery = mergeRawQuery(rt.upstream.RawQuery, req.URL.RawQuery)
			// The proxy token authorizes the hop to the proxy only; it must never
			// be forwarded upstream.
			req.Header.Del(ProxyTokenHeader)
			for k, v := range rt.headers {
				req.Header.Set(k, v)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			name := ""
			if rt, ok := r.Context().Value(routeContextKey{}).(*route); ok {
				name = rt.name
			}
			slog.Error("remote mcp proxy upstream error", "server", name, "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
		},
	}

	return s, nil
}

// pinAwareTransport builds the reverse proxy's RoundTripper. Its DialTLSContext
// reads the per-request route (set by handle) and, when that route carries a
// cert pin, authenticates the upstream by exact SHA-256 fingerprint rather than
// the system trust store — the runtime counterpart of the pinned discovery
// client. Upstreams without a pin get ordinary hostname/chain verification;
// plain-http upstreams never reach here (they use the default DialContext).
// egress, when non-nil, sends the hosts it names through a CONNECT proxy.
// Note this is mutually exclusive with pinning for those hosts: Go ignores
// DialTLSContext on a proxied request. That is not a conflict in practice —
// a pinned upstream is a private self-signed host, which is exactly the kind
// that has no reason to be routed through an external egress.
func pinAwareTransport(egress *egressproxy.Proxy) *http.Transport {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			serverName, _, err := net.SplitHostPort(addr)
			if err != nil {
				serverName = addr
			}

			var cfg *tls.Config
			if rt, ok := ctx.Value(routeContextKey{}).(*route); ok && rt.tlsPin != "" {
				cfg, err = discovery.PinnedTLSConfig(rt.tlsPin)
				if err != nil {
					return nil, err
				}
			} else {
				cfg = &tls.Config{}
			}
			// ServerName drives SNI either way; with a pin, hostname verification
			// is skipped (the pin is stronger), so this only selects the SNI name.
			cfg.ServerName = serverName

			return (&tls.Dialer{Config: cfg}).DialContext(ctx, network, addr)
		},
	}

	// Applied last so it overrides Proxy: an explicitly configured egress
	// route is more specific than whatever the environment happens to say.
	egress.Apply(transport)
	return transport
}

// Token returns the benign per-user token clients must present in the
// ProxyTokenHeader to use the proxy.
func (s *Server) Token() string {
	return s.token
}

// Start begins serving on addr (e.g. "127.0.0.1:0" for a random port) and
// returns the actual listen address.
func (s *Server) Start(addr string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return s.listener.Addr().String(), nil
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}
	s.listener = ln

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)

	s.srv = &http.Server{
		Handler:     mux,
		ReadTimeout: 60 * time.Second,
		// No WriteTimeout: MCP SSE streams are long-lived and the client's
		// context controls cancellation.
		IdleTimeout: 60 * time.Second,
	}
	s.running = true

	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("remote mcp proxy server error", "err", err)
		}
	}()

	slog.Info("remote mcp proxy started", "addr", ln.Addr().String())
	return ln.Addr().String(), nil
}

// Addr returns the listen address, or "" if not running.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// RemoteURL returns the token-free proxy URL the agent config should use for the
// named remote MCP. Empty if the server isn't running.
func (s *Server) RemoteURL(name string) string {
	addr := s.Addr()
	if addr == "" {
		return ""
	}
	return fmt.Sprintf("http://%s/%s", addr, name)
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}
	s.running = false
	return s.srv.Shutdown(ctx)
}

// handle authenticates the proxy hop, resolves the target server and its
// credentials, then forwards to the upstream with auth injected server-side.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(ProxyTokenHeader)), []byte(s.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	name, subpath := splitServerName(r.URL.Path)
	if name == "" {
		http.Error(w, "no server name", http.StatusForbidden)
		return
	}

	remote, err := s.store.GetRemoteMCP(r.Context(), name)
	if err != nil {
		slog.Error("remote mcp proxy: failed to look up server", "name", name, "err", err)
		http.Error(w, "lookup failed", http.StatusBadGateway)
		return
	}
	if remote == nil {
		// Pin the proxy to registered servers only — an unknown name must not
		// become an open relay to an attacker-chosen host.
		http.Error(w, "unknown server", http.StatusForbidden)
		return
	}

	upstream, err := url.Parse(remote.URL)
	if err != nil {
		slog.Error("remote mcp proxy: stored upstream url is invalid", "name", name, "err", err)
		http.Error(w, "invalid upstream", http.StatusBadGateway)
		return
	}

	headers, err := s.resolveAuthHeaders(r.Context(), name, remote.URL)
	if err != nil {
		slog.Error("remote mcp proxy: failed to resolve auth", "name", name, "err", err)
		http.Error(w, "auth unavailable", http.StatusBadGateway)
		return
	}

	rt := &route{name: name, upstream: upstream, subpath: subpath, headers: headers, tlsPin: remote.TLSPinSHA256}
	r = r.WithContext(context.WithValue(r.Context(), routeContextKey{}, rt))
	s.proxy.ServeHTTP(w, r)
}

// resolveAuthHeaders loads the stored credentials for a server, refreshing an
// expired OAuth token on demand, and returns the headers to inject upstream.
// Returns nil headers when the server has no stored auth (e.g. the credential
// lives in a url_secret_key URL, or the server is unauthenticated).
func (s *Server) resolveAuthHeaders(ctx context.Context, name, mcpURL string) (map[string]string, error) {
	auth, err := s.store.GetRemoteMCPAuth(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("load auth: %w", err)
	}
	if auth == nil {
		return nil, nil
	}

	if auth.TokenExpired() && auth.RefreshToken != "" {
		refreshed, refreshErr := s.refreshToken(ctx, name, mcpURL)
		if refreshErr != nil {
			// A failed refresh is not fatal here: forward the (stale) token and
			// let the upstream return 401, rather than blocking the whole server.
			slog.Warn("remote mcp proxy: token refresh failed, using existing token", "name", name, "err", refreshErr)
		} else {
			auth = refreshed
		}
	}

	headers := make(map[string]string, len(auth.StaticHeaders)+1)
	for k, v := range auth.StaticHeaders {
		headers[k] = v
	}
	if auth.AccessToken != "" {
		headers["Authorization"] = "Bearer " + strings.TrimSpace(auth.AccessToken)
	}
	if len(headers) == 0 {
		return nil, nil
	}
	return headers, nil
}

// refreshToken mints a new access token from the stored refresh token and
// persists it. It re-checks expiry under refreshMu so concurrent requests for
// an expired token refresh at most once.
func (s *Server) refreshToken(ctx context.Context, name, mcpURL string) (*remotemcpstore.RemoteMCPAuth, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	auth, err := s.store.GetRemoteMCPAuth(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("reload auth: %w", err)
	}
	if auth == nil {
		return nil, fmt.Errorf("no auth for %q", name)
	}
	if !auth.TokenExpired() || auth.RefreshToken == "" {
		// A concurrent request already refreshed while we waited for the lock.
		return auth, nil
	}

	creds, err := s.refresher(ctx, auth, mcpURL)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}

	auth.AccessToken = creds.AccessToken
	if creds.RefreshToken != "" {
		auth.RefreshToken = creds.RefreshToken
	}
	if creds.ExpiresIn > 0 {
		auth.TokenExpiry = time.Now().Add(time.Duration(creds.ExpiresIn) * time.Second)
	}
	if err := s.store.SetRemoteMCPAuth(ctx, name, auth); err != nil {
		return nil, fmt.Errorf("persist refreshed token: %w", err)
	}
	return auth, nil
}

// defaultRefresher exchanges a refresh token at the stored OAuth token endpoint
// via the discovery client (HTTPS-only, SSRF-guarded).
func defaultRefresher(ctx context.Context, auth *remotemcpstore.RemoteMCPAuth, mcpURL string) (*discovery.RemoteCredentials, error) {
	meta := &discovery.AuthMetadata{
		ResourceURL:           mcpURL,
		AuthorizationEndpoint: auth.AuthorizationEndpoint,
		TokenEndpoint:         auth.TokenEndpoint,
		RegistrationEndpoint:  auth.RegistrationEndpoint,
		Issuer:                auth.AuthServerIssuer,
	}
	reg := &discovery.ClientRegistration{ClientID: auth.ClientID, ClientSecret: auth.ClientSecret}
	return discovery.RefreshRemoteToken(ctx, meta, reg, auth.RefreshToken, mcpURL)
}

// --- helpers ---

// splitServerName splits "/<name>/<subpath>" into the server name and the
// remaining subpath (without a leading slash). Returns an empty name for "/".
func splitServerName(p string) (name, subpath string) {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", ""
	}
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}

// singleJoiningSlash joins a base path and a subpath with exactly one slash.
func singleJoiningSlash(base, sub string) string {
	if sub == "" {
		return base
	}
	baseSlash := strings.HasSuffix(base, "/")
	subSlash := strings.HasPrefix(sub, "/")
	switch {
	case baseSlash && subSlash:
		return base + sub[1:]
	case !baseSlash && !subSlash:
		return base + "/" + sub
	}
	return base + sub
}

// mergeRawQuery combines the upstream URL's own query (which may carry a
// url_secret_key credential) with any query the incoming request added.
func mergeRawQuery(upstream, incoming string) string {
	switch {
	case upstream == "":
		return incoming
	case incoming == "":
		return upstream
	default:
		return upstream + "&" + incoming
	}
}

// generateToken returns a cryptographically random 32-byte hex string.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
