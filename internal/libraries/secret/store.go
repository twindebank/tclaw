// Package secret provides encrypted persistent storage for credentials (OAuth tokens, API keys).
// Three resolution layers exist: config-level ${secret:NAME} references resolve from the OS keychain
// or env vars at load time; the runtime Store encrypts at rest using NaCl secretbox with per-user
// HKDF-derived keys (EncryptedStore for deployed) or macOS Keychain (KeychainStore for local dev);
// and Fly secret seeding bridges env vars into the encrypted store at boot for production.
package secret

import "context"

// Store provides secure persistent storage for secrets (OAuth tokens,
// API keys, etc). Implementations must encrypt at rest.
type Store interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string) error
	Delete(ctx context.Context, key string) error
}
