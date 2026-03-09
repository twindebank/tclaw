package channeltools

import (
	"tclaw/channel"
	"tclaw/mcp"
)

// Deps holds dependencies for channel management tools.
type Deps struct {
	DynamicStore   *channel.DynamicStore
	StaticChannels []channel.Info
}

// RegisterTools adds channel management tools to the MCP handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(channelListDef(), channelListHandler(deps))
	handler.Register(channelCreateDef(), channelCreateHandler(deps))
	handler.Register(channelEditDef(), channelEditHandler(deps))
	handler.Register(channelDeleteDef(), channelDeleteHandler(deps))
}
