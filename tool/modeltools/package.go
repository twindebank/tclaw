package modeltools

import (
	"context"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// Package implements toolpkg.Package for model management tools.
type Package struct{}

func (p *Package) Name() string { return "model" }
func (p *Package) Description() string {
	return "Manage which Claude model the agent uses. List available models, get the current model, or switch to a different one."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupCoreTools }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__model_*"}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec {
	return nil
}

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Bash shell, file operations, web access, and model management."},
		Credentials: nil,
		Tools:       []string{"model_list", "model_get", "model_set"},
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, ctx toolpkg.RegistrationContext) error {
	deps := Deps{Store: ctx.StateStore}
	RegisterTools(handler, deps)
	return nil
}
