// Package knowledgeproxy runs a per-user localhost reverse proxy that gives the
// sandboxed agent token-free git access to a single knowledge-base repo.
//
// The agent's clone points its origin at this proxy over plain HTTP
// (http://127.0.0.1:<port>/<owner>/<repo>.git). The proxy forwards git
// smart-HTTP requests to github.com over HTTPS and injects the GitHub token
// server-side. This grants the agent the capability to pull/push that one repo
// without ever exposing the token: git's credential helper runs inside the
// sandbox, so any token handed to git directly would be readable by the agent —
// injecting auth at the network boundary avoids that entirely.
//
// Requests are pinned to the configured repo and to git's smart-HTTP endpoints,
// so the agent cannot use the proxy to reach other repositories.
package knowledgeproxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// defaultUpstream is the GitHub host git requests are forwarded to. Overridable
// in tests via Config.Upstream.
const defaultUpstream = "https://github.com"

// TokenSource returns the GitHub token used to authenticate forwarded requests.
// Called per request so token rotation takes effect without a restart.
type TokenSource func(ctx context.Context) (string, error)

// Config configures a knowledge-base proxy server.
type Config struct {
	// RepoPath is the GitHub repo path without leading slash or ".git" suffix,
	// e.g. "owner/knowledge-base". All other paths are rejected.
	RepoPath string

	// Token supplies the GitHub PAT injected into forwarded requests.
	Token TokenSource

	// Upstream is the base URL git requests are forwarded to. Defaults to
	// https://github.com; overridden in tests.
	Upstream string
}

// tokenContextKey carries the resolved token from the request handler to the
// reverse-proxy Director without threading it through struct fields.
type tokenContextKey struct{}

// Server is a running knowledge-base proxy.
type Server struct {
	repoPath    string
	tokenSource TokenSource
	upstream    *url.URL
	proxy       *httputil.ReverseProxy

	listener net.Listener
	srv      *http.Server

	mu      sync.Mutex
	running bool
}

// NewServer builds a proxy server from cfg. It does not start listening until
// Start is called.
func NewServer(cfg Config) (*Server, error) {
	if cfg.RepoPath == "" {
		return nil, fmt.Errorf("repo path is required")
	}
	if cfg.Token == nil {
		return nil, fmt.Errorf("token source is required")
	}

	upstreamRaw := cfg.Upstream
	if upstreamRaw == "" {
		upstreamRaw = defaultUpstream
	}
	upstream, err := url.Parse(upstreamRaw)
	if err != nil {
		return nil, fmt.Errorf("parse upstream %q: %w", upstreamRaw, err)
	}

	s := &Server{
		repoPath:    strings.Trim(cfg.RepoPath, "/"),
		tokenSource: cfg.Token,
		upstream:    upstream,
	}

	s.proxy = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = upstream.Scheme
			req.URL.Host = upstream.Host
			req.Host = upstream.Host
			// Replace any credentials git may have attached with the server-side
			// token. Git-over-HTTPS to GitHub uses HTTP Basic auth (the token as
			// the username, empty password) — the same scheme repotools relies on
			// via https://<token>@github.com. The REST "Authorization: token …"
			// scheme is NOT accepted by the git smart-HTTP endpoints.
			if token, ok := req.Context().Value(tokenContextKey{}).(string); ok && token != "" {
				req.SetBasicAuth(token, "")
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			slog.Error("knowledge proxy upstream error", "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
		},
	}

	return s, nil
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
		// No WriteTimeout: large fetches/pushes can stream for a while and the
		// client's context controls cancellation.
		IdleTimeout: 60 * time.Second,
	}
	s.running = true

	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("knowledge proxy server error", "err", err)
		}
	}()

	slog.Info("knowledge proxy started", "addr", ln.Addr().String(), "repo", s.repoPath)
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

// RemoteURL returns the token-free git remote URL the agent's clone should use.
// Empty if the server isn't running.
func (s *Server) RemoteURL() string {
	addr := s.Addr()
	if addr == "" {
		return ""
	}
	return fmt.Sprintf("http://%s/%s.git", addr, s.repoPath)
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

// handle validates that the request targets the pinned repo and a git
// smart-HTTP endpoint, resolves the token, then proxies to the upstream.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if !s.allowedRequest(r.URL.Path, r.URL.Query().Get("service")) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	token, err := s.tokenSource(r.Context())
	if err != nil {
		slog.Error("knowledge proxy: failed to resolve token", "err", err)
		http.Error(w, "auth unavailable", http.StatusBadGateway)
		return
	}
	// Guard against a stray newline/whitespace in the stored secret — a trailing
	// newline corrupts the Authorization header and yields a 401 from GitHub.
	token = strings.TrimSpace(token)
	if token == "" {
		slog.Error("knowledge proxy: no github token available")
		http.Error(w, "auth unavailable", http.StatusBadGateway)
		return
	}

	r = r.WithContext(context.WithValue(r.Context(), tokenContextKey{}, token))
	s.proxy.ServeHTTP(w, r)
}

// allowedRequest reports whether a request path (and info/refs service param)
// targets the pinned repo and a permitted git smart-HTTP endpoint.
func (s *Server) allowedRequest(path, service string) bool {
	prefix := "/" + s.repoPath + ".git/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	switch strings.TrimPrefix(path, prefix) {
	case "info/refs":
		// info/refs advertises refs for either a fetch or a push.
		return service == "git-upload-pack" || service == "git-receive-pack"
	case "git-upload-pack", "git-receive-pack":
		return true
	default:
		return false
	}
}
