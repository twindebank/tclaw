package provider

import (
	"tclaw/connection"
)

// AuthType identifies how a provider authenticates.
type AuthType string

const (
	AuthNone   AuthType = "none"
	AuthOAuth2 AuthType = "oauth2"
	AuthAPIKey AuthType = "api_key"
)

// OAuth2Config holds the OAuth2 settings for a provider.
type OAuth2Config struct {
	AuthURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
	ExtraParams  map[string]string // e.g. access_type=offline
}

// Provider defines a service that tclaw can connect to.
type Provider struct {
	ID     connection.ProviderID
	Name   string // human-readable ("Gmail", "Linear")
	Auth   AuthType
	OAuth2 *OAuth2Config // nil if Auth != AuthOAuth2
}

// Registry holds all known providers, keyed by ID.
type Registry struct {
	providers map[connection.ProviderID]*Provider
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[connection.ProviderID]*Provider)}
}

// Register adds a provider to the registry.
func (r *Registry) Register(p *Provider) {
	r.providers[p.ID] = p
}

// Get returns a provider by ID, or nil if not found.
func (r *Registry) Get(id connection.ProviderID) *Provider {
	return r.providers[id]
}

// List returns all registered provider IDs.
func (r *Registry) List() []connection.ProviderID {
	ids := make([]connection.ProviderID, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	return ids
}
