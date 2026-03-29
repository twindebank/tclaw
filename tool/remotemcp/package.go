package remotemcp

import (
	"context"
	"fmt"

	"tclaw/claudecli"
	"tclaw/connection"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// ExtraKeyConnManager is the RegistrationContext.Extra key for *connection.Manager.
const ExtraKeyConnManager = "connection_manager"

// ExtraKeyConfigUpdater is the RegistrationContext.Extra key for the config updater callback.
const ExtraKeyConfigUpdater = "remote_mcp_config_updater"

// Package implements toolpkg.Package for remote MCP server management tools.
type Package struct{}

func (p *Package) Name() string { return "remote_mcp" }
func (p *Package) Description() string {
	return "Manage remote MCP server connections: add, remove, list, and authorize external tool servers."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupConnections }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__remote_mcp_*"}
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
	connMgr, ok := regCtx.Extra[ExtraKeyConnManager].(*connection.Manager)
	if !ok || connMgr == nil {
		return fmt.Errorf("remotemcp: missing %s in Extra", ExtraKeyConnManager)
	}

	var configUpdater func(context.Context) error
	if fn, ok := regCtx.Extra[ExtraKeyConfigUpdater].(func(context.Context) error); ok {
		configUpdater = fn
	}

	deps := Deps{
		Manager:       connMgr,
		Callback:      regCtx.Callback,
		ConfigUpdater: configUpdater,
	}

	RegisterTools(handler, deps)
	if regCtx.Callback != nil {
		RegisterAuthWaitTool(handler, deps)
	}

	return nil
}
