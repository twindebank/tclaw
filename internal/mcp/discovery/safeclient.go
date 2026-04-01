package discovery

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	// httpTimeout is the per-request timeout for all outbound HTTP calls
	// during discovery and token exchange.
	httpTimeout = 30 * time.Second

	// maxResponseBodyBytes caps the amount of data read from any discovery
	// response to prevent memory exhaustion from oversized payloads.
	maxResponseBodyBytes = 1 << 20 // 1 MiB
)

// safeClient is an http.Client with timeouts that refuses to connect to
// private/loopback IP addresses, preventing SSRF attacks during MCP discovery.
var safeClient = &http.Client{
	Timeout: httpTimeout,
	Transport: &http.Transport{
		DialContext:           safeDialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	},
}

// safeDialContext resolves DNS and validates that the target IP is not
// private/loopback before connecting. This prevents DNS rebinding attacks
// where a domain resolves to a public IP during validation but to a
// private IP when the actual connection is made.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}

	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS lookup failed for %q: %w", host, err)
	}

	// Try each resolved IP, rejecting private addresses.
	var lastErr error
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if isPrivateIP(ip) {
			lastErr = fmt.Errorf("refusing to connect to private IP %s for host %q", ipStr, host)
			continue
		}
		conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ipStr, port))
		if err != nil {
			lastErr = err
			continue
		}
		return conn, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no valid IPs for host %q", host)
}

// validateExternalURL checks that a URL is safe to fetch during discovery:
// - Must be HTTPS (except in tests)
// - Must not resolve to a private/loopback/link-local address
func validateExternalURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "https" {
		return fmt.Errorf("only HTTPS URLs are allowed (got %q)", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("URL has no hostname")
	}

	// Resolve hostname to IPs and block private ranges.
	ips, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("DNS lookup failed for %q: %w", hostname, err)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if isPrivateIP(ip) {
			return fmt.Errorf("URL %q resolves to private/loopback address %s — refusing to connect", rawURL, ipStr)
		}
	}

	return nil
}

// isPrivateIP returns true if the IP is loopback, private, link-local,
// or any other non-globally-routable address.
func isPrivateIP(ip net.IP) bool {
	// Loopback (127.0.0.0/8, ::1)
	if ip.IsLoopback() {
		return true
	}
	// Link-local (169.254.0.0/16, fe80::/10)
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// Unspecified (0.0.0.0, ::)
	if ip.IsUnspecified() {
		return true
	}

	// RFC 1918 private ranges + other reserved ranges.
	privateRanges := []struct {
		network string
	}{
		{"10.0.0.0/8"},
		{"172.16.0.0/12"},
		{"192.168.0.0/16"},
		{"fc00::/7"},      // unique local addresses
		{"100.64.0.0/10"}, // carrier-grade NAT (RFC 6598)
	}

	for _, r := range privateRanges {
		_, cidr, err := net.ParseCIDR(r.network)
		if err != nil {
			continue
		}
		if cidr.Contains(ip) {
			return true
		}
	}

	return false
}
