package google

import (
	"context"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// Package implements toolpkg.Package for Google Workspace tools.
//
// Google is a provider-based package: tools are registered dynamically
// per-connection by the router's OnProviderConnect callback. The Package
// interface provides metadata only — Register is a no-op because connection
// lifecycle is managed by connectiontools.
type Package struct{}

func (p *Package) Name() string { return "google" }
func (p *Package) Description() string {
	return "Google Workspace: Gmail (list, read, send), Calendar (list, create), and workspace data access. Requires an OAuth connection via connection_add."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupGSuiteWrite }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__google_*"}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec {
	// Google uses OAuth connections, not direct secrets.
	return nil
}

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Google Workspace full access."},
		Credentials: nil,
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, regCtx toolpkg.RegistrationContext) error {
	// No-op: Google tools are registered dynamically per-connection by the
	// router's OnProviderConnect callback. This package provides metadata
	// and info tool only.
	return nil
}
