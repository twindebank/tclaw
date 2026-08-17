package egressproxy_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/egressproxy"
)

func TestNew(t *testing.T) {
	valid := egressproxy.Config{
		URL:   "http://egress.internal:8000",
		Token: "secret",
		Hosts: []string{"mcp.example.com"},
	}

	t.Run("accepts a complete config", func(t *testing.T) {
		p, err := egressproxy.New(valid)
		require.NoError(t, err)
		require.NotNil(t, p)
	})

	tests := []struct {
		name    string
		mutate  func(*egressproxy.Config)
		wantErr string
	}{
		{
			name:    "rejects a missing url",
			mutate:  func(c *egressproxy.Config) { c.URL = "" },
			wantErr: "url is required",
		},
		{
			name:    "rejects a non-http scheme",
			mutate:  func(c *egressproxy.Config) { c.URL = "socks5://egress.internal:1080" },
			wantErr: "must be http or https",
		},
		{
			name:    "rejects a url with no host",
			mutate:  func(c *egressproxy.Config) { c.URL = "http://" },
			wantErr: "no host",
		},
		{
			// An unauthenticated proxy on a private network is usable by
			// anything else on that network.
			name:    "rejects a missing token",
			mutate:  func(c *egressproxy.Config) { c.Token = "" },
			wantErr: "token is required",
		},
		{
			name:    "rejects an empty host list",
			mutate:  func(c *egressproxy.Config) { c.Hosts = nil },
			wantErr: "at least one host",
		},
		{
			name:    "rejects a blank host entry",
			mutate:  func(c *egressproxy.Config) { c.Hosts = []string{"mcp.example.com", "  "} },
			wantErr: "empty entry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			cfg.Hosts = append([]string(nil), valid.Hosts...)
			tt.mutate(&cfg)

			_, err := egressproxy.New(cfg)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestProxyFunc(t *testing.T) {
	p, err := egressproxy.New(egressproxy.Config{
		URL:   "http://egress.internal:8000",
		Token: "secret",
		Hosts: []string{"mcp.example.com", "www.example.com"},
	})
	require.NoError(t, err)
	proxyFunc := p.ProxyFunc()

	tests := []struct {
		name    string
		url     string
		proxied bool
	}{
		{name: "listed host is proxied", url: "https://mcp.example.com/mcp", proxied: true},
		{name: "second listed host is proxied", url: "https://www.example.com/token", proxied: true},
		{name: "listed host with explicit port is proxied", url: "https://mcp.example.com:443/mcp", proxied: true},
		{name: "host match is case-insensitive", url: "https://MCP.Example.COM/mcp", proxied: true},
		{name: "unlisted host goes direct", url: "https://api.telegram.org/bot", proxied: false},
		{name: "subdomain of a listed host goes direct", url: "https://evil.mcp.example.com/", proxied: false},
		{name: "suffix lookalike goes direct", url: "https://notmcp.example.com/", proxied: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			got, err := proxyFunc(req)
			require.NoError(t, err)
			if tt.proxied {
				require.NotNil(t, got, "expected %s to route through the proxy", tt.url)
				require.Equal(t, "egress.internal:8000", got.Host)
				return
			}
			require.Nil(t, got, "expected %s to connect directly", tt.url)
		})
	}
}

func TestConnectHeader(t *testing.T) {
	t.Run("carries the bearer token", func(t *testing.T) {
		p, err := egressproxy.New(egressproxy.Config{
			URL: "http://egress.internal:8000", Token: "s3cret", Hosts: []string{"mcp.example.com"},
		})
		require.NoError(t, err)

		require.Equal(t, "Bearer s3cret", p.ConnectHeader().Get("Proxy-Authorization"))
	})

	t.Run("a nil proxy yields no header", func(t *testing.T) {
		var p *egressproxy.Proxy
		require.Nil(t, p.ConnectHeader())
	})
}

func TestApply(t *testing.T) {
	t.Run("sets proxy and connect header on the transport", func(t *testing.T) {
		p, err := egressproxy.New(egressproxy.Config{
			URL: "http://egress.internal:8000", Token: "tok", Hosts: []string{"mcp.example.com"},
		})
		require.NoError(t, err)

		transport := &http.Transport{}
		p.Apply(transport)

		require.NotNil(t, transport.Proxy)
		require.Equal(t, "Bearer tok", transport.ProxyConnectHeader.Get("Proxy-Authorization"))
	})

	t.Run("a nil proxy leaves the transport connecting directly", func(t *testing.T) {
		var p *egressproxy.Proxy
		transport := &http.Transport{}
		p.Apply(transport)

		require.Nil(t, transport.Proxy)
		require.Nil(t, transport.ProxyConnectHeader)
	})
}

func TestRoutes(t *testing.T) {
	t.Run("a nil proxy routes nothing", func(t *testing.T) {
		var p *egressproxy.Proxy
		require.False(t, p.Routes("mcp.example.com"))
	})

	t.Run("matches with or without a port", func(t *testing.T) {
		p, err := egressproxy.New(egressproxy.Config{
			URL: "http://egress.internal:8000", Token: "tok", Hosts: []string{"mcp.example.com"},
		})
		require.NoError(t, err)

		require.True(t, p.Routes("mcp.example.com"))
		require.True(t, p.Routes("mcp.example.com:443"))
		require.False(t, p.Routes("other.example.com:443"))
	})
}
