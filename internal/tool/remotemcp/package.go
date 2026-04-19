package remotemcp

import (
	"context"

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
	deps := Deps{
		Manager:       p.Manager,
		Callback:      p.Callback,
		SecretStore:   regCtx.SecretStore,
		ConfigUpdater: p.ConfigUpdater,
	}

	RegisterTools(handler, deps)
	if p.Callback != nil {
		RegisterAuthWaitTool(handler, deps)
	}

	return nil
}
