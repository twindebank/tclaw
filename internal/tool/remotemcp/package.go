package remotemcp

import (
	"context"
	"log/slog"

	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/oauth"
	"tclaw/internal/remotemcpstore"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

// Package implements toolpkg.Package for remote MCP server management tools.
type Package struct {
	Manager       *remotemcpstore.Manager
	Callback      *oauth.CallbackServer
	ConfigUpdater func(context.Context) error

	// ChannelNames lists the channels that exist, so the tools can refuse to
	// scope a server to a name that matches none of them.
	ChannelNames func() []string

	// OnChannelChange is invoked after a remote MCP is added or removed so
	// the router can restart the running agent — the Claude CLI reads the
	// tool allowlist at process-start time only, so without a restart the
	// new tools stay invisible until the next idle timeout.
	OnChannelChange func()
}

func (p *Package) Name() string { return "remote_mcp" }
func (p *Package) Description() string {
	return "Manage remote MCP server connections: add, remove, list, and authorize external tool servers."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupConnections }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__remote_mcp_*"},
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec { return nil }

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Manage OAuth connections and remote MCP servers."},
		Credentials: nil,
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, regCtx toolpkg.RegistrationContext) error {
	if p.Manager == nil {
		// Every tool here reads the manager, so registering them without one
		// would panic on the first call. The one-shot CLI builds the registry
		// without it.
		slog.Warn("remote mcp tools not registered: no manager configured")
		return nil
	}

	deps := Deps{
		Manager:         p.Manager,
		Callback:        p.Callback,
		SecretStore:     regCtx.SecretStore,
		ConfigUpdater:   p.ConfigUpdater,
		OnChannelChange: p.OnChannelChange,
		ChannelNames:    p.ChannelNames,
	}

	RegisterTools(handler, deps)
	if p.Callback != nil {
		RegisterAuthWaitTool(handler, deps)
	}

	return nil
}
