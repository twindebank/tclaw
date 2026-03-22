package channeltools

import (
	"tclaw/channel"
	"tclaw/config"
	"tclaw/libraries/secret"
	"tclaw/mcp"
)

// Deps holds dependencies for channel management tools.
type Deps struct {
	Registry *channel.Registry
	Env      config.Env

	SecretStore secret.Store

	// ConfigPath is the path to the active tclaw.yaml. Included in error
	// messages when the agent tries to edit a static channel, so it knows
	// exactly which file to modify.
	ConfigPath string

	// OnChannelChange is called after a channel is created, edited, or deleted.
	// The router uses this to trigger an automatic agent restart so the new
	// channel configuration takes effect immediately.
	OnChannelChange func()
}

// RegisterTools adds channel management tools to the MCP handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(channelListDef(), channelListHandler(deps))
	handler.Register(channelCreateDef(), channelCreateHandler(deps))
	handler.Register(channelEditDef(), channelEditHandler(deps))
	handler.Register(channelDeleteDef(), channelDeleteHandler(deps))
}
