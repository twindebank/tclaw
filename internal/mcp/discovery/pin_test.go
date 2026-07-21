package discovery

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePin(t *testing.T) {
	valid := strings.Repeat("ab", 32) // 64 hex chars = 32 bytes

	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"plain hex", valid, false},
		{"uppercase", strings.ToUpper(valid), false},
		{"colon-separated", insertColons(valid), false},
		{"too short", "abcd", true},
		{"not hex", strings.Repeat("zz", 32), true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := ParsePin(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, raw, sha256.Size)
		})
	}
}

func TestPinnedTLSConfig(t *testing.T) {
	t.Run("empty pin returns nil so default verification is kept", func(t *testing.T) {
		cfg, err := PinnedTLSConfig("")
		require.NoError(t, err)
		require.Nil(t, cfg)
	})

	t.Run("connects when the server cert matches the pin", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "pinned-ok")
		}))
		defer srv.Close()

		cfg, err := PinnedTLSConfig(fingerprint(srv))
		require.NoError(t, err)
		body := getWithTLS(t, srv.URL, cfg)
		require.Equal(t, "pinned-ok", body)
	})

	t.Run("rejects a server whose cert does not match the pin", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "should-not-reach")
		}))
		defer srv.Close()

		cfg, err := PinnedTLSConfig(strings.Repeat("cd", 32)) // a valid-shape but wrong pin
		require.NoError(t, err)

		client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
		_, err = client.Get(srv.URL)
		require.Error(t, err)
		require.Contains(t, err.Error(), "pin mismatch")
	})
}

// --- helpers ---

func fingerprint(srv *httptest.Server) string {
	sum := sha256.Sum256(srv.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

func getWithTLS(t *testing.T, url string, cfg *tls.Config) string {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func insertColons(hexStr string) string {
	var b strings.Builder
	for i := 0; i < len(hexStr); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(hexStr[i : i+2])
	}
	return b.String()
}
