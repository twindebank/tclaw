package restauranttools

import (
	"tclaw/libraries/secret"
	"tclaw/mcp"
)

// Deps holds dependencies for restaurant tools.
type Deps struct {
	SecretStore secret.Store

	// OnCredentialsStored is called after credentials are successfully stored
	// via restaurant_set_credentials. The router uses this to register the
	// operational tools that require authentication.
	OnCredentialsStored func()
}

// RegisterInfoTools registers only the setup/info tool (restaurant_set_credentials).
// Always registered so the agent can describe the provider and collect credentials.
func RegisterInfoTools(handler *mcp.Handler, deps Deps) {
	providers := buildProviders(deps)
	for _, def := range infoToolDefs {
		handler.Register(def, makeHandler(def.Name, providers, deps))
	}
}

// RegisterTools registers the operational restaurant tools (search, book, etc.).
// Only call this when credentials are known to be configured.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	providers := buildProviders(deps)
	for _, def := range operationalToolDefs {
		handler.Register(def, makeHandler(def.Name, providers, deps))
	}
}

// UnregisterTools removes all restaurant tools (info + operational) from the handler.
func UnregisterTools(handler *mcp.Handler) {
	for _, def := range infoToolDefs {
		handler.Unregister(def.Name)
	}
	for _, def := range operationalToolDefs {
		handler.Unregister(def.Name)
	}
}

// buildProviders creates the provider registry. Currently Resy only —
// add new providers here as they're implemented.
func buildProviders(deps Deps) map[string]Provider {
	resy := NewResyProvider(deps.SecretStore)
	return map[string]Provider{
		resy.Name(): resy,
	}
}
