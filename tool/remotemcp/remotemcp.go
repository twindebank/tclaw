package remotemcp

import (
	"context"

	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/remotemcpstore"
)

// Deps holds dependencies for remote MCP management tools.
type Deps struct {
	Manager  *remotemcpstore.Manager
	Callback *oauth.CallbackServer // nil if OAuth callback is not configured

	// ConfigUpdater is called after a remote MCP is added or removed to
	// regenerate the MCP config file. The next Claude turn picks up the change.
	ConfigUpdater func(ctx context.Context) error
}

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolRemoteMCPList, ToolRemoteMCPAdd, ToolRemoteMCPRemove, ToolRemoteMCPAuthWait,
	}
}

// RegisterTools adds the remote MCP management tools to the MCP handler.
func RegisterTools(h *mcp.Handler, deps Deps) {
	h.Register(remoteMCPListDef(), remoteMCPListHandler(deps))
	h.Register(remoteMCPAddDef(), remoteMCPAddHandler(deps))
	h.Register(remoteMCPRemoveDef(), remoteMCPRemoveHandler(deps))
}

// RegisterAuthWaitTool adds the remote_mcp_auth_wait tool.
func RegisterAuthWaitTool(h *mcp.Handler, deps Deps) {
	h.Register(remoteMCPAuthWaitDef(), remoteMCPAuthWaitHandler(deps))
}
