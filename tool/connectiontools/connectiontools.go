package connectiontools

import (
	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/provider"
)

// Deps holds the dependencies for connection management tools.
type Deps struct {
	Manager  *connection.Manager
	Registry *provider.Registry
	Callback *oauth.CallbackServer // nil if OAuth is not configured
	Handler  *mcp.Handler          // MCP handler for dynamic tool registration

	// OnProviderConnect is called when an OAuth flow completes so the
	// caller can register provider-specific tools dynamically. Avoids
	// importing provider tool packages from here.
	OnProviderConnect func(connID connection.ConnectionID, mgr *connection.Manager, p *provider.Provider)
}

// RegisterTools adds the connection management tools to the MCP handler.
// These are always available regardless of which providers are connected.
func RegisterTools(h *mcp.Handler, deps Deps) {
	h.Register(connectionListDef(), connectionListHandler(deps.Manager))
	h.Register(connectionProvidersDef(), connectionProvidersHandler(deps.Registry))
	h.Register(connectionAddDef(), connectionAddHandler(deps))
	h.Register(connectionRemoveDef(), connectionRemoveHandler(deps.Manager))
}

// RegisterAuthWaitTool adds the connection_auth_wait tool. Separated from
// RegisterTools because it's only useful when OAuth is configured.
func RegisterAuthWaitTool(h *mcp.Handler, mgr *connection.Manager) {
	h.Register(connectionAuthWaitDef(), connectionAuthWaitHandler(mgr))
}
