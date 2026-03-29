package channeltools

import (
	"context"
	"fmt"

	"tclaw/channel"
	"tclaw/claudecli"
	"tclaw/config"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// Extra keys for RegistrationContext.Extra.
const (
	ExtraKeyRegistry        = "channel_registry"
	ExtraKeyEnv             = "channel_env"
	ExtraKeyOnChannelAdded  = "on_channel_added"
	ExtraKeyOnChannelChange = "on_channel_change"
	ExtraKeyActivityTracker = "activity_tracker"
	ExtraKeyProvisioners    = "channel_provisioners"
	ExtraKeyActiveChannel   = "active_channel"

	// Send/SendWhenFree deps.
	ExtraKeyLinks         = "channel_links"
	ExtraKeyCrossChOutput = "cross_channel_output"
	ExtraKeyChannelsFunc  = "channels_func"
	ExtraKeyPendingStore  = "pending_store"
)

// Package implements toolpkg.Package for channel management and messaging tools.
type Package struct{}

func (p *Package) Name() string { return "channel" }
func (p *Package) Description() string {
	return "Channel lifecycle management (create, edit, delete, list, notify, done), " +
		"cross-channel messaging (send, send_when_free, is_busy), and tool group listing."
}

func (p *Package) Group() toolgroup.ToolGroup {
	// Channel tools span two groups: management and messaging. We return
	// the broader one; both groups' patterns include these tools.
	return toolgroup.GroupChannelManagement
}

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{
		"mcp__tclaw__channel_*",
		"mcp__tclaw__tool_list",
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec { return nil }

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Full channel lifecycle."},
		Credentials: nil,
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, regCtx toolpkg.RegistrationContext) error {
	// Core channel management tools.
	registry, ok := regCtx.Extra[ExtraKeyRegistry].(*channel.Registry)
	if !ok || registry == nil {
		return fmt.Errorf("channeltools: missing %s in Extra", ExtraKeyRegistry)
	}

	env, _ := regCtx.Extra[ExtraKeyEnv].(config.Env)
	var onChannelAdded func(string)
	if fn, ok := regCtx.Extra[ExtraKeyOnChannelAdded].(func(string)); ok {
		onChannelAdded = fn
	}
	var onChannelChange func()
	if fn, ok := regCtx.Extra[ExtraKeyOnChannelChange].(func()); ok {
		onChannelChange = fn
	}
	var activityTracker *channel.ActivityTracker
	if at, ok := regCtx.Extra[ExtraKeyActivityTracker].(*channel.ActivityTracker); ok {
		activityTracker = at
	}
	var provisioners map[channel.ChannelType]channel.EphemeralProvisioner
	if prov, ok := regCtx.Extra[ExtraKeyProvisioners].(map[channel.ChannelType]channel.EphemeralProvisioner); ok {
		provisioners = prov
	}
	var activeChannel func() string
	if fn, ok := regCtx.Extra[ExtraKeyActiveChannel].(func() string); ok {
		activeChannel = fn
	}

	RegisterTools(handler, Deps{
		Registry:        registry,
		Env:             env,
		SecretStore:     regCtx.SecretStore,
		ConfigPath:      regCtx.ConfigPath,
		OnChannelAdded:  onChannelAdded,
		OnChannelChange: onChannelChange,
		ActivityTracker: activityTracker,
		Provisioners:    provisioners,
		ActiveChannel:   activeChannel,
	})

	// Cross-channel send tools.
	var linksFunc func() map[string][]channel.Link
	if fn, ok := regCtx.Extra[ExtraKeyLinks].(func() map[string][]channel.Link); ok {
		linksFunc = fn
	}
	var crossChOutput chan<- channel.TaggedMessage
	if ch, ok := regCtx.Extra[ExtraKeyCrossChOutput].(chan<- channel.TaggedMessage); ok {
		crossChOutput = ch
	}
	var channelsFunc func() map[channel.ChannelID]channel.Channel
	if fn, ok := regCtx.Extra[ExtraKeyChannelsFunc].(func() map[channel.ChannelID]channel.Channel); ok {
		channelsFunc = fn
	}

	if linksFunc != nil && crossChOutput != nil && channelsFunc != nil && activeChannel != nil {
		RegisterSendTool(handler, SendDeps{
			Links:         linksFunc,
			Output:        crossChOutput,
			Channels:      channelsFunc,
			ActiveChannel: activeChannel,
		})

		var pendingStore *channel.PendingStore
		if ps, ok := regCtx.Extra[ExtraKeyPendingStore].(*channel.PendingStore); ok {
			pendingStore = ps
		}
		if pendingStore != nil && activityTracker != nil {
			RegisterSendWhenFreeTool(handler, SendWhenFreeDeps{
				Links:           linksFunc,
				Output:          crossChOutput,
				Channels:        channelsFunc,
				ActiveChannel:   activeChannel,
				ActivityTracker: activityTracker,
				PendingStore:    pendingStore,
			})
		}
	}

	// tool_list registered last so it can see all tools.
	RegisterToolListTool(handler)

	return nil
}
