package bankingtools

import (
	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/oauth"
)

const (
	// ApplicationIDStoreKey is the secret store key for the Enable Banking app ID.
	ApplicationIDStoreKey = "enablebanking_app_id"

	// PrivateKeyStoreKey is the secret store key for the Enable Banking RSA private key PEM.
	PrivateKeyStoreKey = "enablebanking_private_key"
)

// Deps holds dependencies for banking tools.
type Deps struct {
	SecretStore secret.Store
	StateStore  store.Store
	Callback    *oauth.CallbackServer
}

// RegisterTools registers all banking tools on the handler.
// Each call creates a new handlerState, so per-user pending flows are isolated.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	state := &handlerState{
		deps:     deps,
		sessions: NewSessionStore(deps.StateStore),
	}
	for _, def := range toolDefs {
		handler.Register(def, makeHandler(def.Name, state))
	}
}

// UnregisterTools removes all banking tools from the handler.
func UnregisterTools(handler *mcp.Handler) {
	for _, def := range toolDefs {
		handler.Unregister(def.Name)
	}
}
