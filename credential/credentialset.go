// Package credential provides a unified credential storage model for tool packages.
//
// A CredentialSet is a named, optionally channel-scoped collection of secrets
// that a tool package needs to function. It replaces both the old SecretSpec
// system (for API keys) and the Connection system (for OAuth tokens) with a
// single abstraction.
//
// Each credential set is identified by "<package>/<label>" (e.g. "google/work",
// "tfl/default") and stores its fields in the encrypted secret store at
// "cred/<package>/<label>/<field>".
package credential

import (
	"fmt"
	"time"
)

// CredentialSetID uniquely identifies a credential set: "<package>/<label>".
type CredentialSetID string

// NewCredentialSetID builds a CredentialSetID from a package name and label.
func NewCredentialSetID(packageName string, label string) CredentialSetID {
	return CredentialSetID(fmt.Sprintf("%s/%s", packageName, label))
}

// CredentialSet is a named collection of credentials for a tool package.
// A user may have multiple sets for the same package (e.g. "google/work" and
// "google/personal"), each optionally scoped to a specific channel.
type CredentialSet struct {
	ID      CredentialSetID `json:"id"`
	Package string          `json:"package"`
	Label   string          `json:"label"`

	// Channel scopes this credential set to a specific channel. When set,
	// the tool package's tools are only available on that channel. Empty
	// means available on all channels.
	Channel string `json:"channel,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// OAuthTokens holds OAuth2 tokens for a credential set.
type OAuthTokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
}

// Expired reports whether the access token has expired (with 1-minute buffer).
func (t OAuthTokens) Expired() bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(t.ExpiresAt.Add(-1 * time.Minute))
}
