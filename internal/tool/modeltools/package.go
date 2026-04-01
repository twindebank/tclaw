package modeltools

import (
	"context"

	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

// Package implements toolpkg.Package for model management tools.
type Package struct {
	Store store.Store
}

func (p *Package) Name() string { return "model" }
func (p *Package) Description() string {
	return "Manage which Claude model the agent uses. List available models, get the current model, or switch to a different one."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupCoreTools }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__model_*"},
	}
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
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, ctx toolpkg.RegistrationContext) error {
	deps := Deps{Store: p.Store}
	RegisterTools(handler, deps)
	return nil
}
