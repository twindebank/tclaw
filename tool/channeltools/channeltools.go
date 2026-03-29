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

	// OnChannelAdded is called after a new channel is created with the channel's
	// name. Unlike OnChannelChange (which triggers a full restart), this signals
	// a hot-add: the router wires in the new channel without restarting existing
	// sessions. If nil, falls back to OnChannelChange for compatibility.
	OnChannelAdded func(name string)

	// OnChannelChange is called after a channel is edited or deleted, or when
	// a channel is created and OnChannelAdded is nil. The router uses this to
	// trigger an automatic agent restart so the new channel configuration takes
	// effect immediately.
	OnChannelChange func()

	// ActivityTracker tracks per-channel processing state for channel_is_busy.
	ActivityTracker *channel.ActivityTracker

	// Provisioners maps channel types to their EphemeralProvisioner. Used by
	// channel_create (when no explicit token is provided) and channel_done
	// (for platform-specific cleanup). May be nil if no provisioners are configured.
	Provisioners map[channel.ChannelType]channel.EphemeralProvisioner

	// ActiveChannel returns the name of the channel currently being processed.
	// Used by channel_create to look up the creating channel's creatable_roles.
	ActiveChannel func() string
}

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolChannelList, ToolChannelCreate, ToolChannelEdit, ToolChannelDelete,
		ToolChannelIsBusy, ToolChannelDone, ToolChannelNotify,
		ToolChannelSend, ToolChannelSendWhenFree, ToolList, ToolGroupList,
	}
}

// RegisterTools adds channel management tools to the MCP handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(channelListDef(), channelListHandler(deps))
	handler.Register(channelCreateDef(), channelCreateHandler(deps))
	handler.Register(channelEditDef(), channelEditHandler(deps))
	handler.Register(channelDeleteDef(), channelDeleteHandler(deps))
	handler.Register(channelIsBusyDef(), channelIsBusyHandler(deps))
	handler.Register(channelDoneDef(), channelDoneHandler(deps))
	handler.Register(channelNotifyDef(), channelNotifyHandler(deps))
	handler.Register(toolGroupListDef(), toolGroupListHandler())
}
