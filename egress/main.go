// Command egress is an authenticated HTTP CONNECT proxy whose own traffic
// leaves through a Mullvad WireGuard tunnel.
//
// It exists because Strava blocks tclaw's Fly egress IP at its load balancer:
// every request to mcp.strava.com from Fly gets a bare 403 from awselb, while
// the same request from a UK residential or Mullvad IP gets the normal 401
// OAuth challenge. Routing just that upstream through a Mullvad exit restores
// access without touching tclaw's own networking.
//
// CONNECT rather than a reverse proxy is deliberate: the client's TLS runs
// end-to-end to Strava, so this process never sees an OAuth token, and the MCP
// URL stays https://mcp.strava.com/mcp — which matters because the OAuth
// `resource` parameter (RFC 8707) is bound to that exact URL.
//
// Two controls keep the proxy from becoming an open relay on Fly's 6PN: a
// bearer token, and a destination allowlist. Both fail closed.
package main

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// dialTimeout bounds the connection to the upstream, which is reached
	// through the WireGuard tunnel and so is slower than a direct dial.
	dialTimeout = 30 * time.Second

	// idleTimeout closes a tunnelled connection that has gone quiet. MCP
	// streams can be long-lived, so this is generous.
	idleTimeout = 10 * time.Minute
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	config, err := loadConfig()
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:        config.listenAddr,
		Handler:     http.HandlerFunc(config.handle),
		ReadTimeout: 0, // CONNECT tunnels outlive any request deadline.
	}

	slog.Info("egress proxy listening",
		"addr", config.listenAddr, "allowed_hosts", config.allowedHosts)
	if err := server.ListenAndServe(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

type config struct {
	listenAddr   string
	token        string
	allowedHosts []string
}

// loadConfig reads configuration from the environment, refusing to start on
// anything missing. A proxy that boots without a token or without an allowlist
// would be an open relay, so both are hard requirements rather than defaults.
func loadConfig() (*config, error) {
	token := os.Getenv("EGRESS_TOKEN")
	if token == "" {
		return nil, errors.New("EGRESS_TOKEN is required — refusing to run an unauthenticated proxy")
	}

	raw := os.Getenv("EGRESS_ALLOWED_HOSTS")
	if raw == "" {
		return nil, errors.New("EGRESS_ALLOWED_HOSTS is required — refusing to run an open relay")
	}
	var hosts []string
	for _, h := range strings.Split(raw, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) == 0 {
		return nil, errors.New("EGRESS_ALLOWED_HOSTS contained no usable entries")
	}

	addr := os.Getenv("EGRESS_LISTEN_ADDR")
	if addr == "" {
		addr = ":8000"
	}

	return &config{listenAddr: addr, token: token, allowedHosts: hosts}, nil
}

func (c *config) handle(w http.ResponseWriter, r *http.Request) {
	// A plain GET /healthz lets Fly check liveness without a token: it
	// reveals nothing and proves only that the process is up.
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
		return
	}

	if r.Method != http.MethodConnect {
		http.Error(w, "only CONNECT is supported", http.StatusMethodNotAllowed)
		return
	}

	if !c.authorized(r) {
		// Logged because a silent refusal is indistinguishable from the
		// request never arriving, which turns a wrong token into a puzzling
		// 403 with nothing to correlate against. The credential itself is
		// never logged — only that one was presented, and its length, which
		// is enough to tell an unresolved placeholder from a real token.
		slog.Warn("refused unauthorized CONNECT",
			"target", r.Host,
			"credential_presented", presentedCredential(r) != "",
			"credential_length", len(presentedCredential(r)))
		// Deliberately terse to the caller: an unauthenticated client learns
		// nothing about what this proxy fronts.
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	target := r.Host
	if !c.allowed(target) {
		slog.Warn("refused destination outside allowlist", "target", target)
		http.Error(w, "destination not allowed", http.StatusForbidden)
		return
	}

	// tcp4, not tcp: the WireGuard tunnel carries IPv4 only, because Fly's
	// private 6PN is IPv6 and has to keep working. Dialling v6 here would
	// leave through Fly's own address — the one the upstream blocks — and
	// would do it silently.
	upstream, err := net.DialTimeout("tcp4", target, dialTimeout)
	if err != nil {
		slog.Error("upstream dial failed", "target", target, "err", err)
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		slog.Error("connection cannot be hijacked, CONNECT unsupported by this server")
		http.Error(w, "cannot tunnel", http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		slog.Error("hijack failed", "err", err)
		return
	}
	defer client.Close()

	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		slog.Error("failed to acknowledge CONNECT", "target", target, "err", err)
		return
	}

	slog.Info("tunnel open", "target", target)
	tunnel(client, upstream)
}

// authorized checks the bearer token in constant time. Both Proxy-Authorization
// (what an HTTP client sends to a proxy) and Authorization are accepted, since
// callers differ in which they set.
func (c *config) authorized(r *http.Request) bool {
	presented := presentedCredential(r)
	if presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(c.token)) == 1
}

// presentedCredential extracts the bearer credential from either header a
// client might use, returning empty when none is present or the Bearer prefix
// is missing.
func presentedCredential(r *http.Request) string {
	header := r.Header.Get("Proxy-Authorization")
	if header == "" {
		header = r.Header.Get("Authorization")
	}
	trimmed := strings.TrimPrefix(header, "Bearer ")
	if trimmed == header {
		// No Bearer prefix — not a form we accept.
		return ""
	}
	return trimmed
}

// allowed reports whether target ("host:port") is in the allowlist. Entries
// may name a bare host (any port) or an explicit host:port.
func (c *config) allowed(target string) bool {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return false
	}
	for _, entry := range c.allowedHosts {
		if entry == target {
			return true
		}
		if entry == host && (port == "443" || port == "80") {
			return true
		}
	}
	return false
}

// tunnel copies bytes in both directions until either side closes, then tears
// the other down so a half-closed peer cannot leak a goroutine.
func tunnel(client, upstream net.Conn) {
	done := make(chan struct{}, 2)

	copyDir := func(dst, src net.Conn, direction string) {
		if err := setIdleDeadline(dst, src); err != nil {
			slog.Warn("could not set tunnel deadline", "direction", direction, "err", err)
		}
		if _, err := io.Copy(dst, src); err != nil && !isExpectedTunnelEnd(err) {
			slog.Warn("tunnel copy ended", "direction", direction, "err", err)
		}
		done <- struct{}{}
	}

	go copyDir(upstream, client, "client->upstream")
	go copyDir(client, upstream, "upstream->client")

	// One direction finishing means the tunnel is over; closing both unblocks
	// the other copy.
	<-done
	client.Close()
	upstream.Close()
	<-done
}

func setIdleDeadline(dst, src net.Conn) error {
	deadline := time.Now().Add(idleTimeout)
	if err := dst.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set dst deadline: %w", err)
	}
	if err := src.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set src deadline: %w", err)
	}
	return nil
}

// isExpectedTunnelEnd reports whether err is the ordinary way a tunnel ends —
// one side closing — rather than something worth logging as a problem.
func isExpectedTunnelEnd(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF)
}
