// Package gitproxy runs a per-user localhost reverse proxy that gives the
// sandboxed agent token-free git access to the repos it is allowed to reach,
// at the access level each has been granted.
//
// Every clone points its origin at this proxy over plain HTTP
// (http://127.0.0.1:<port>/<name>.git). The proxy resolves the name to a real
// GitHub repo, forwards git smart-HTTP requests over HTTPS, and injects the
// token server-side. Git's credential helper runs inside the sandbox, so any
// token handed to git directly would be readable by the agent — injecting auth
// at the network boundary avoids that entirely.
//
// The upstream host is fixed at github.com, which is what makes credential
// exfiltration structurally impossible rather than merely disallowed: there is
// no code path that sends a token anywhere else, whatever URL a repo claims.
//
// Access tiers are enforced here rather than in the tools because the agent runs
// git itself. Anything checked tool-side could be bypassed with raw git; the
// transport cannot be.
package gitproxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"tclaw/internal/repo"
)

// defaultUpstream is the GitHub host git requests are forwarded to. Overridable
// in tests via Config.Upstream.
const defaultUpstream = "https://github.com"

// Repo is what the proxy needs to know to serve one tracked repo.
type Repo struct {
	// Path is the upstream repo path without leading slash or ".git" suffix,
	// e.g. "owner/config".
	Path string

	// Access is the tier in force, already accounting for expiry.
	Access repo.Access

	// DefaultBranch is the branch protected at the pull_requests_only tier.
	DefaultBranch string

	// Token authenticates requests for this repo. Empty means the repo is
	// fetched unauthenticated, which only works for public repos.
	Token string
}

// Lookup resolves a proxy path segment to the repo it serves. Returning nil
// means no such repo, which the proxy answers with 404 — the agent should not
// learn whether a name exists but is scoped away from it.
type Lookup func(ctx context.Context, name string) (*Repo, error)

// Config configures a git proxy server.
type Config struct {
	// Repos resolves names to repos. Called per request so a tier change or a
	// newly added repo takes effect without a restart.
	Repos Lookup

	// Upstream is the base URL git requests are forwarded to. Defaults to
	// https://github.com; overridden in tests.
	Upstream string
}

// requestRepoKey carries the resolved repo from the handler to the Director
// without threading it through struct fields.
type requestRepoKey struct{}

// Server is a running git proxy.
type Server struct {
	repos    Lookup
	upstream *url.URL
	proxy    *httputil.ReverseProxy

	listener net.Listener
	srv      *http.Server

	mu      sync.Mutex
	running bool
}

// NewServer builds a proxy server from cfg. It does not listen until Start.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Repos == nil {
		return nil, fmt.Errorf("repo lookup is required")
	}

	upstreamRaw := cfg.Upstream
	if upstreamRaw == "" {
		upstreamRaw = defaultUpstream
	}
	upstream, err := url.Parse(upstreamRaw)
	if err != nil {
		return nil, fmt.Errorf("parse upstream %q: %w", upstreamRaw, err)
	}

	s := &Server{repos: cfg.Repos, upstream: upstream}

	s.proxy = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = upstream.Scheme
			req.URL.Host = upstream.Host
			req.Host = upstream.Host

			resolved, _ := req.Context().Value(requestRepoKey{}).(*Repo)
			if resolved == nil {
				return
			}
			req.URL.Path = resolved.upstreamPath(req.URL.Path)

			// Replace any credentials git may have attached with the
			// server-side token. Git-over-HTTPS to GitHub uses HTTP Basic auth
			// with the token as the username; the REST "Authorization: token …"
			// scheme is NOT accepted by the git smart-HTTP endpoints.
			if resolved.Token != "" {
				req.SetBasicAuth(resolved.Token, "")
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			slog.Error("git proxy upstream error", "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
		},
	}

	return s, nil
}

// upstreamPath rewrites "/<name>.git/<endpoint>" to the real repo's path.
func (r Repo) upstreamPath(path string) string {
	_, endpoint, ok := splitProxyPath(path)
	if !ok {
		return path
	}
	return "/" + r.Path + ".git/" + endpoint
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
			slog.Error("git proxy server error", "err", err)
		}
	}()

	slog.Info("git proxy started", "addr", ln.Addr().String())
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

// RemoteURL returns the token-free git remote a clone of the named repo should
// use. Empty if the server isn't running.
func (s *Server) RemoteURL(name string) string {
	addr := s.Addr()
	if addr == "" {
		return ""
	}
	return fmt.Sprintf("http://%s/%s.git", addr, name)
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

// handle resolves the repo, checks the operation against its access tier, then
// proxies upstream.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	name, endpoint, ok := splitProxyPath(r.URL.Path)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	operation, ok := classify(endpoint, r.URL.Query().Get("service"))
	if !ok {
		// Only git's smart-HTTP endpoints are proxied, so the agent cannot use
		// this as a general-purpose route to github.com.
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	resolved, err := s.repos(r.Context(), name)
	if err != nil {
		slog.Error("git proxy: failed to resolve repo", "repo", name, "err", err)
		http.Error(w, "repo lookup failed", http.StatusBadGateway)
		return
	}
	if resolved == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if operation == operationPush {
		if err := s.checkPush(r, resolved); err != nil {
			slog.Warn("git proxy: push refused", "repo", name, "access", resolved.Access, "reason", err)
			// 403 with the reason in the body: git surfaces it to the agent,
			// so it learns why rather than retrying blindly.
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}

	r = r.WithContext(context.WithValue(r.Context(), requestRepoKey{}, resolved))
	s.proxy.ServeHTTP(w, r)
}

// checkPush enforces the repo's access tier on a push, rewinding the request
// body so the inspected bytes still reach the upstream.
func (s *Server) checkPush(r *http.Request, resolved *Repo) error {
	switch resolved.Access {
	case repo.AccessFullWrite:
		return nil
	case repo.AccessPullRequestsOnly:
		// Fall through to inspect which refs are being written.
	default:
		return fmt.Errorf("this repo is read-only — use repo_sync to refresh it, " +
			"and ask for write access if you need to make changes")
	}

	// The ref advertisement carries no refs to inspect; the commands arrive in
	// the git-receive-pack request that follows, which is where the decision is
	// made. Allowing the advertisement lets git report the refusal properly.
	if r.Method != http.MethodPost {
		return nil
	}

	// A body we cannot read is a body whose effects we cannot bound, so an
	// encoding we do not decode is refused rather than passed through.
	if encoding := r.Header.Get("Content-Encoding"); encoding != "" {
		return fmt.Errorf("push with unsupported Content-Encoding %q cannot be checked against this repo's access level", encoding)
	}

	commands, consumed, err := parseRefCommands(r.Body)
	if err != nil {
		return fmt.Errorf("could not read the ref updates in this push, so it was refused: %w", err)
	}

	// Replay what was consumed ahead of the packfile still on the wire.
	r.Body = struct {
		io.Reader
		io.Closer
	}{
		Reader: io.MultiReader(bytes.NewReader(consumed), r.Body),
		Closer: r.Body,
	}

	return checkPullRequestsOnly(commands, resolved.DefaultBranch)
}

// operation is the kind of git request being made.
type operation int

const (
	operationFetch operation = iota
	operationPush
)

// classify maps a smart-HTTP endpoint to the operation it performs. Returns
// false for anything that is not part of the smart-HTTP protocol.
func classify(endpoint, service string) (operation, bool) {
	switch endpoint {
	case "info/refs":
		// info/refs advertises refs for either a fetch or a push; the service
		// parameter says which, and a push advertisement is refused for a
		// read-only repo so git fails early with a clear message.
		switch service {
		case "git-upload-pack":
			return operationFetch, true
		case "git-receive-pack":
			return operationPush, true
		default:
			return 0, false
		}
	case "git-upload-pack":
		return operationFetch, true
	case "git-receive-pack":
		return operationPush, true
	default:
		return 0, false
	}
}

// splitProxyPath splits "/<name>.git/<endpoint>" into its parts.
func splitProxyPath(path string) (name, endpoint string, ok bool) {
	trimmed := strings.TrimPrefix(path, "/")
	suffix := ".git/"
	idx := strings.Index(trimmed, suffix)
	if idx <= 0 {
		return "", "", false
	}
	name = trimmed[:idx]
	endpoint = trimmed[idx+len(suffix):]
	// A name with a slash in it would let a crafted URL address a different
	// upstream path than the one the repo resolves to.
	if name == "" || endpoint == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return name, endpoint, true
}
