package connection

import (
	"fmt"
	"time"
)

// ProviderID identifies a service provider (e.g. "gmail", "linear").
type ProviderID string

// ConnectionID uniquely identifies a connection: "<provider>/<label>".
type ConnectionID string

// NewConnectionID builds a ConnectionID from a provider and label.
func NewConnectionID(provider ProviderID, label string) ConnectionID {
	return ConnectionID(fmt.Sprintf("%s/%s", provider, label))
}

// Connection is an authenticated instance of a provider.
// A user may have multiple connections to the same provider
// (e.g. "gmail/work" and "gmail/personal").
type Connection struct {
	ID         ConnectionID
	ProviderID ProviderID
	Label      string // user-chosen namespace ("work", "personal")
	CreatedAt  time.Time
}

// Credentials holds OAuth2 or API key credentials for a connection.
type Credentials struct {
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time         `json:"expires_at,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"` // provider-specific
}

// Expired reports whether the access token has expired (with 1-minute buffer).
func (c Credentials) Expired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(c.ExpiresAt.Add(-1 * time.Minute))
}
