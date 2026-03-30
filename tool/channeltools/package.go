package channeltools

import (
	"context"

	"tclaw/channel"
	"tclaw/claudecli"
	"tclaw/config"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/reconciler"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
	"tclaw/user"
)

// Package implements toolpkg.Package for channel management and messaging tools.
type Package struct {
	Registry        *channel.Registry
	RuntimeState    *channel.RuntimeStateStore
	Env             config.Env
	SecretStore     secret.Store
	ConfigPath      string
	UserID          user.ID
	OnChannelAdded  func(string)
	OnChannelChange func()
	ActivityTracker *channel.ActivityTracker
	Provisioners    map[channel.ChannelType]channel.EphemeralProvisioner
	ActiveChannel   func() string

	// Send deps.
	Links    func() map[string][]channel.Link
	Output   chan<- channel.TaggedMessage
	Channels func() map[channel.ChannelID]channel.Channel
}

func (p *Package) Name() string { return "channel" }
func (p *Package) Description() string {
	return "Channel lifecycle management (create, edit, delete, list, notify, done), " +
		"cross-channel messaging (send, is_busy), and tool group listing."
}

func (p *Package) Group() toolgroup.ToolGroup {
	// Channel tools span two groups: management and messaging. We return
	// the broader one; both groups' patterns include these tools.
	return toolgroup.GroupChannelManagement
}

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		toolgroup.GroupChannelManagement: {
			"mcp__tclaw__channel_create",
			"mcp__tclaw__channel_delete",
			"mcp__tclaw__channel_edit",
			"mcp__tclaw__channel_list",
			"mcp__tclaw__channel_notify",
			"mcp__tclaw__channel_done",
			"mcp__tclaw__channel_is_busy",
			"mcp__tclaw__channel_send",
		},
		toolgroup.GroupChannelMessaging: {
			"mcp__tclaw__channel_send",
			"mcp__tclaw__channel_is_busy",
			"mcp__tclaw__channel_done",
			// Read-only — lets channels discover available tool groups without
			// needing the full channel_management group.
			"mcp__tclaw__tool_group_list",
		},
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
	configWriter := config.NewWriter(p.ConfigPath, p.Env)

	RegisterTools(handler, Deps{
		Registry:     p.Registry,
		ConfigWriter: configWriter,
		RuntimeState: p.RuntimeState,
		UserID:       p.UserID,
		Env:          p.Env,
		SecretStore:  p.SecretStore,
		ConfigPath:   p.ConfigPath,
		ReconcileParams: reconciler.ReconcileParams{
			Channels:     nil, // Populated from config at runtime.
			SecretStore:  p.SecretStore,
			RuntimeState: p.RuntimeState,
			Provisioners: p.Provisioners,
		},
		OnChannelAdded:  p.OnChannelAdded,
		OnChannelChange: p.OnChannelChange,
		ActivityTracker: p.ActivityTracker,
		Provisioners:    p.Provisioners,
		ActiveChannel:   p.ActiveChannel,
	})

	// Cross-channel send tools.
	if p.Links != nil && p.Output != nil && p.Channels != nil && p.ActiveChannel != nil {
		RegisterSendTool(handler, SendDeps{
			Links:         p.Links,
			Output:        p.Output,
			Channels:      p.Channels,
			ActiveChannel: p.ActiveChannel,
		})
	}

	// tool_list registered last so it can see all tools.
	RegisterToolListTool(handler)

	return nil
}
