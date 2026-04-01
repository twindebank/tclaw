package bankingtools

import (
	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/oauth"
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

	// OnCredentialsStored is called after credentials are successfully stored
	// via banking_set_credentials. The router uses this to register the
	// operational tools that require authentication.
	OnCredentialsStored func()
}

// RegisterInfoTools registers only the setup/info tool (banking_set_credentials).
// Always registered so the agent can describe the provider and collect credentials.
func RegisterInfoTools(handler *mcp.Handler, deps Deps) {
	state := &handlerState{
		deps:     deps,
		sessions: NewSessionStore(deps.StateStore),
	}
	for _, def := range infoToolDefs {
		handler.Register(def, makeHandler(def.Name, state))
	}
}

// RegisterTools registers the operational banking tools (list_banks, connect, etc.).
// Each call creates a new handlerState, so per-user pending flows are isolated.
// Only call this when credentials are known to be configured.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	state := &handlerState{
		deps:     deps,
		sessions: NewSessionStore(deps.StateStore),
	}
	for _, def := range operationalToolDefs {
		handler.Register(def, makeHandler(def.Name, state))
	}
}

// UnregisterTools removes all banking tools (info + operational) from the handler.
func UnregisterTools(handler *mcp.Handler) {
	for _, def := range infoToolDefs {
		handler.Unregister(def.Name)
	}
	for _, def := range operationalToolDefs {
		handler.Unregister(def.Name)
	}
}
