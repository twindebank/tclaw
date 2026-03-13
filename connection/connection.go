package connection

import (
	"fmt"
	"time"

	"tclaw/provider"
)

// ConnectionID uniquely identifies a connection: "<provider>/<label>".
type ConnectionID string

// NewConnectionID builds a ConnectionID from a provider and label.
func NewConnectionID(providerID provider.ProviderID, label string) ConnectionID {
	return ConnectionID(fmt.Sprintf("%s/%s", providerID, label))
}

// Connection is an authenticated instance of a provider.
// A user may have multiple connections to the same provider
// (e.g. "gmail/work" and "gmail/personal").
type Connection struct {
	ID         ConnectionID        `json:"id"`
	ProviderID provider.ProviderID `json:"provider_id"`
	Label      string              `json:"label"` // user-chosen namespace ("work", "personal")

	// Channel scopes this connection to a specific channel. Provider tools
	// (e.g. google_*) are only available on this channel.
	Channel string `json:"channel"`

	CreatedAt time.Time `json:"created_at"`
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
