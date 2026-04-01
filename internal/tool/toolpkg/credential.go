package toolpkg

import (
	"tclaw/internal/credential"
	"tclaw/internal/mcp"
)

// AuthType identifies how credentials for a tool package are acquired.
type AuthType string

const (
	// AuthAPIKey means credentials are collected via a secret form (API keys,
	// tokens, etc.). The agent triggers a secret_form_request when a tool
	// returns CREDENTIALS_NEEDED.
	AuthAPIKey AuthType = "api_key"

	// AuthOAuth2 means the package requires an OAuth2 browser flow after
	// initial setup fields (client_id, client_secret) are provided.
	AuthOAuth2 AuthType = "oauth2"
)

// CredentialSpec declares the credential requirements for a tool package.
// It replaces both SecretSpec (for API keys) and the old provider/connection
// system (for OAuth) with a single declaration.
type CredentialSpec struct {
	// AuthType determines how credentials are acquired and what flows
	// the credential tools use when setting up this package.
	AuthType AuthType

	// Fields declares the individual secrets this package needs. For OAuth
	// packages, these are the setup credentials (client_id, client_secret).
	// For API key packages, these are the keys themselves.
	Fields []CredentialField

	// OAuth is non-nil only when AuthType is AuthOAuth2. It holds the
	// OAuth2 endpoints and scopes owned by this tool package.
	OAuth *OAuthSpec

	// SupportsMultiple indicates whether multiple named credential sets
	// are useful (e.g. "google/work" + "google/personal"). When false,
	// the system uses a single implicit "default" label.
	SupportsMultiple bool

	// ConfigKey is the path in tclaw.yaml for backward-compatible credential
	// seeding (e.g. "providers.google"). If set, credentials are seeded
	// from config at startup. Empty means no config seeding.
	ConfigKey string
}

// CredentialField describes a single secret field within a credential set.
type CredentialField struct {
	// Key is the field identifier within the credential set (e.g. "client_id",
	// "api_key"). Used as the storage key suffix: cred/<pkg>/<label>/<key>.
	Key string

	// Label is a human-readable name (e.g. "TfL API Key").
	Label string

	// Description is shown to help the user understand what this field is
	// and how to obtain it.
	Description string

	// Required indicates whether the package needs this field to function.
	// Optional fields allow degraded operation (e.g. TfL works without an
	// API key but is rate-limited).
	Required bool

	// EnvVarPrefix is the env var prefix for seeding from Fly secrets.
	// Combined with the user ID: EnvVarPrefix + "_" + UPPER(userID).
	// Example: "TFL_API_KEY" -> "TFL_API_KEY_THEO".
	EnvVarPrefix string
}

// OAuthSpec holds OAuth2 configuration owned by the tool package. This
// replaces the old provider.OAuth2Config — the tool package is the single
// owner of its OAuth integration details.
type OAuthSpec struct {
	AuthURL  string
	TokenURL string
	Scopes   []string

	// ExtraParams are additional query parameters sent during the OAuth
	// authorization request (e.g. "access_type": "offline").
	ExtraParams map[string]string

	// Services is a human-readable list of services this OAuth connection
	// unlocks (e.g. "Gmail", "Google Calendar"). Shown in credential_list
	// and the info tool.
	Services []string
}

// RequiredFieldKeys returns the keys of all required fields.
func (s CredentialSpec) RequiredFieldKeys() []string {
	var keys []string
	for _, f := range s.Fields {
		if f.Required {
			keys = append(keys, f.Key)
		}
	}
	return keys
}

// AllFieldKeys returns the keys of all fields.
func (s CredentialSpec) AllFieldKeys() []string {
	keys := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		keys[i] = f.Key
	}
	return keys
}

// NeedsOAuth returns true if this spec includes an OAuth flow.
func (s CredentialSpec) NeedsOAuth() bool {
	return s.OAuth != nil
}

// ResolvedCredentialSet pairs a credential set with its readiness status.
// Passed to OnCredentialSetChange so packages know which sets are ready.
type ResolvedCredentialSet struct {
	credential.CredentialSet

	// Ready is true when all required fields are populated and (for OAuth)
	// tokens are present.
	Ready bool
}

// CredentialProvider is an optional interface that tool packages implement
// to declare their credential requirements in the unified model. Packages
// that don't need credentials continue to use the base Package interface.
//
// When a package implements CredentialProvider, the router calls
// OnCredentialSetChange instead of Register for tool registration.
type CredentialProvider interface {
	Package

	// CredentialSpec returns the credential requirements for this package.
	CredentialSpec() CredentialSpec

	// OnCredentialSetChange is called when the set of credentials for this
	// package changes (added, removed, or fields updated). The package
	// should register or unregister its tools on the handler based on which
	// sets are ready.
	OnCredentialSetChange(handler *mcp.Handler, ctx RegistrationContext, sets []ResolvedCredentialSet) error
}
