package discovery

import (
	"net"
	"testing"
)

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip      string
		private bool
	}{
		// Loopback
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"::1", true},

		// RFC 1918 private ranges
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"192.168.255.255", true},

		// Link-local
		{"169.254.1.1", true},
		{"fe80::1", true},

		// Carrier-grade NAT
		{"100.64.0.1", true},
		{"100.127.255.255", true},

		// Unspecified
		{"0.0.0.0", true},
		{"::", true},

		// Public IPs
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"203.0.113.1", false},
		{"2607:f8b0:4004:800::200e", false}, // Google IPv6
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			got := isPrivateIP(ip)
			if got != tt.private {
				t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.private)
			}
		})
	}
}

func TestValidateExternalURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		// Valid HTTPS URLs (must resolve to public IPs)
		{"https://mcp.linear.app/sse", false},
		{"https://www.google.com/mcp", false},

		// HTTP rejected
		{"http://example.com/mcp", true},

		// Private/loopback rejected
		{"https://localhost/mcp", true},
		{"https://127.0.0.1/mcp", true},

		// Invalid URLs
		{"not-a-url", true},
		{"", true},
		{"ftp://example.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := validateExternalURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateExternalURL(%q) error = %v, wantErr = %v", tt.url, err, tt.wantErr)
			}
		})
	}
}
