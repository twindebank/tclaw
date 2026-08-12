// Package secret provides encrypted persistent storage for credentials (OAuth tokens, API keys).
// Three resolution layers exist: config-level ${boot:NAME} references resolve from the OS keychain
// or env vars at load time; the runtime Store encrypts at rest using NaCl secretbox with per-user
// HKDF-derived keys (EncryptedStore for deployed) or macOS Keychain (KeychainStore for local dev);
// and Fly secret seeding bridges env vars into the encrypted store at boot for production.
//
// # Key namespaces
//
// Keys are namespaced by who owns them, so that one writer cannot reach another's values:
//
//	cred/<type>/<label>/<field>  credential slots declared in config, plus the OAuth tokens and
//	                             API keys their flows collect. Written by boot seeding, OAuth
//	                             callbacks and the credential tools.
//	channel/<name>/token         per-channel transport tokens, written by channel provisioners.
//	<bare_key>                   values a tool package declares via RequiredSecrets, and the
//	                             ad-hoc keys the agent names for itself (e.g. a remote MCP URL).
//
// The separation is structural rather than policed: agent-facing surfaces validate keys against
// `^[a-z0-9_]+$` (see secretform), which cannot express a slash, so nothing the agent names can
// land inside cred/ or channel/. Operator credentials therefore belong in a slot — a bare key is
// reachable by any tool that can request a secret form.
package secret

import "context"

const (
	// CredentialPrefix namespaces credential slot fields. Callers should build keys with the
	// credential package rather than concatenating this themselves.
	CredentialPrefix = "cred/"

	// ChannelPrefix namespaces per-channel transport tokens. See channel.ChannelSecretKey.
	ChannelPrefix = "channel/"
)

// Store provides secure persistent storage for secrets (OAuth tokens,
// API keys, etc). Implementations must encrypt at rest.
type Store interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string) error
	Delete(ctx context.Context, key string) error
}
