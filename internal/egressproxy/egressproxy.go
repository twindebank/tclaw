// Package egressproxy routes outbound requests for named hosts through an
// authenticated HTTP CONNECT proxy, leaving every other destination on the
// default path.
//
// It exists because some upstreams refuse tclaw's hosting provider by IP: they
// answer a datacenter address with a bare 403 while serving the same request
// normally from a residential or VPN exit. Sending just those hosts through a
// proxy with an acceptable exit address restores them without moving any other
// traffic off its direct route.
//
// The host list is operator configuration only. Nothing the agent can say
// selects, adds to, or bypasses it — an agent-settable proxy would let a
// prompt-injected turn route an authenticated upstream through an attacker's
// listener.
package egressproxy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Config declares the proxy and which hosts must use it.
type Config struct {
	// URL is the CONNECT proxy, e.g. http://egress.internal:8000.
	URL string

	// Token authenticates tclaw to the proxy. Sent as a bearer credential on
	// the CONNECT request, never to the destination.
	Token string

	// Hosts are the destination hostnames to route through the proxy. Matching
	// is exact on the hostname; port is ignored.
	Hosts []string
}

// Proxy decides, per request, whether to route through the CONNECT proxy.
type Proxy struct {
	proxyURL *url.URL
	token    string
	hosts    map[string]struct{}
}

// New validates the configuration and builds a Proxy. It rejects anything
// incomplete rather than silently degrading: a misconfigured egress proxy
// means the affected upstream goes back to being blocked, and doing that
// quietly would surface much later as an unexplained 403.
func New(cfg Config) (*Proxy, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("egress proxy url is required")
	}
	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse egress proxy url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("egress proxy url must be http or https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("egress proxy url has no host")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("egress proxy token is required — an unauthenticated proxy would accept anyone on the private network")
	}
	if len(cfg.Hosts) == 0 {
		return nil, fmt.Errorf("egress proxy requires at least one host to route, otherwise it would never be used")
	}

	hosts := make(map[string]struct{}, len(cfg.Hosts))
	for _, h := range cfg.Hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			return nil, fmt.Errorf("egress proxy host list contains an empty entry")
		}
		hosts[h] = struct{}{}
	}

	return &Proxy{proxyURL: parsed, token: cfg.Token, hosts: hosts}, nil
}

// Routes reports whether host is one this proxy handles.
func (p *Proxy) Routes(host string) bool {
	if p == nil {
		return false
	}
	// A host:port pair arrives from transports; compare the hostname alone.
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	_, ok := p.hosts[strings.ToLower(host)]
	return ok
}

// ProxyFunc returns a function suitable for http.Transport.Proxy. It returns
// nil for hosts outside the list, which tells the transport to connect
// directly.
func (p *Proxy) ProxyFunc() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if p == nil || req.URL == nil || !p.Routes(req.URL.Hostname()) {
			return nil, nil
		}
		return p.proxyURL, nil
	}
}

// ConnectHeader returns the headers to send on the CONNECT request. These
// authenticate tclaw to the proxy and are never forwarded to the destination,
// which sees only the tunnelled TLS session.
func (p *Proxy) ConnectHeader() http.Header {
	if p == nil {
		return nil
	}
	h := http.Header{}
	h.Set("Proxy-Authorization", "Bearer "+p.token)
	return h
}

// Apply wires the proxy into a transport. Safe to call with a nil Proxy, which
// leaves the transport connecting directly to everything.
func (p *Proxy) Apply(t *http.Transport) {
	if p == nil || t == nil {
		return
	}
	t.Proxy = p.ProxyFunc()
	t.ProxyConnectHeader = p.ConnectHeader()
}
