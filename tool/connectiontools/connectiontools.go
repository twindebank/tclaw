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

	// OnGmailConnect is called when a Gmail OAuth flow completes so the
	// caller can register Gmail tools dynamically. Avoids importing the
	// gmail sub-package from here.
	OnGmailConnect func(connID connection.ConnectionID, mgr *connection.Manager, p *provider.Provider)
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
