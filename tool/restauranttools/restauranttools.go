package restauranttools

import (
	"tclaw/libraries/secret"
	"tclaw/mcp"
)

// Deps holds dependencies for restaurant tools.
type Deps struct {
	SecretStore secret.Store
}

// RegisterTools registers the restaurant tools on the handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	providers := buildProviders(deps)
	for _, def := range toolDefs {
		handler.Register(def, makeHandler(def.Name, providers, deps))
	}
}

// UnregisterTools removes the restaurant tools from the handler.
func UnregisterTools(handler *mcp.Handler) {
	for _, def := range toolDefs {
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
