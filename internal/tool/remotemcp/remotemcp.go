package remotemcp

import (
	"context"

	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/oauth"
	"tclaw/internal/remotemcpstore"
)

// Deps holds dependencies for remote MCP management tools.
type Deps struct {
	Manager  *remotemcpstore.Manager
	Callback *oauth.CallbackServer // nil if OAuth callback is not configured

	// SecretStore resolves header values from keys passed via
	// `header_secret_keys` on remote_mcp_add. Required for the secret-form
	// flow where the agent collects Cloudflare Access (or similar)
	// credentials via secret_form_request before registering the MCP.
	SecretStore secret.Store

	// ConfigUpdater is called after a remote MCP is added or removed to
	// regenerate the MCP config file. The next Claude turn picks up the change.
	ConfigUpdater func(ctx context.Context) error

	// HomeDir is the user's HOME directory (e.g. <base>/<userID>/home). Used
	// to locate settings.json so that new MCP tool patterns are automatically
	// added to the permissions.allow list on registration.
	HomeDir string
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
