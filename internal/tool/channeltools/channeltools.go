package channeltools

import (
	"tclaw/internal/channel"
	"tclaw/internal/config"
	"tclaw/internal/dev"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/reconciler"
	"tclaw/internal/user"
)

// Deps holds dependencies for channel management tools.
type Deps struct {
	Registry     *channel.Registry
	ConfigWriter *config.Writer
	RuntimeState *channel.RuntimeStateStore
	UserID       user.ID
	Env          config.Env

	SecretStore secret.Store

	// ConfigPath is the path to the active tclaw.yaml. Included in error
	// messages so the user knows which file to check.
	ConfigPath string

	// MemoryDir is the user's memory directory root. Used to clean up
	// per-channel knowledge dirs on channel deletion.
	MemoryDir string

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

	// Provisioners returns the EphemeralProvisioner for a channel type, or nil.
	// May be nil if no provisioners are configured.
	Provisioners channel.ProvisionerLookup

	// ReconcileParams provides dependencies for synchronous reconciliation
	// after config mutations. Channel tools call the reconciler to provision
	// channels immediately so the agent gets feedback on success/failure.
	ReconcileParams reconciler.ReconcileParams

	// ActiveChannel returns the name of the channel currently being processed.
	ActiveChannel func() string

	// DevStore is used by channel_delete to tear down any dev sessions that
	// were started from the channel being deleted. May be nil in tests or in
	// environments where dev tools aren't configured.
	DevStore *dev.Store
}

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolChannelList, ToolChannelCreate, ToolChannelEdit, ToolChannelDelete,
		ToolChannelIsBusy, ToolChannelDone, ToolChannelNotify,
		ToolChannelSend, ToolChannelTranscript, ToolList, ToolGroupList,
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
