package discovery

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ParsePin decodes and validates a hex SHA-256 certificate fingerprint,
// tolerating colons and mixed case (openssl prints AA:BB:...). Returns the 32
// raw bytes.
func ParsePin(pinHex string) ([]byte, error) {
	clean := strings.ToLower(strings.ReplaceAll(pinHex, ":", ""))
	raw, err := hex.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("tls pin must be hex (optionally colon-separated): %w", err)
	}
	if len(raw) != sha256.Size {
		return nil, fmt.Errorf("tls pin must be a SHA-256 fingerprint (%d hex chars), got %d bytes", sha256.Size*2, len(raw))
	}
	return raw, nil
}

// PinnedTLSConfig returns a *tls.Config that authenticates the server by its
// leaf certificate's SHA-256 fingerprint instead of the system trust store.
// This is for self-signed certs on Fly private hosts, where no public CA
// applies: pinning the fingerprint (a non-secret) both encrypts and
// authenticates the connection without trusting the network. An empty pin
// returns a nil config so the caller keeps default chain verification.
func PinnedTLSConfig(pinHex string) (*tls.Config, error) {
	if pinHex == "" {
		return nil, nil
	}
	want, err := ParsePin(pinHex)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		// Chain verification is replaced by an exact fingerprint match below, so
		// the cert needs no CA and its hostname need not match — the pin is the
		// stronger, sufficient check.
		InsecureSkipVerify: true, //nolint:gosec // VerifyConnection pins the exact leaf cert.
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("tls pin: server presented no certificate")
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(sum[:], want) != 1 {
				return fmt.Errorf("tls pin mismatch: server certificate does not match the expected fingerprint")
			}
			return nil
		},
	}, nil
}

// NewPinnedSafeClient builds an SSRF-safe http.Client (the same private-IP
// guard as the default discovery client) that additionally pins the server
// certificate to pinHex when non-empty. Used at registration time to list a
// pinned server's tools over its self-signed TLS.
func NewPinnedSafeClient(pinHex string) (*http.Client, error) {
	tlsCfg, err := PinnedTLSConfig(pinHex)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: httpTimeout,
		Transport: NewColdStartRetryTransport(&http.Transport{
			DialContext:           safeDialContext,
			TLSClientConfig:       tlsCfg,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		}, DefaultColdStartRetry),
	}, nil
}
