package ruletools

import (
	"context"

	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

// Package implements toolpkg.Package for the rulebook pool.
type Package struct {
	// ArmRuleWrite asks the user to confirm a proposed rulebook change.
	ArmRuleWrite func(ctx context.Context, request RuleWriteRequest) error
}

func (p *Package) Name() string { return "rule" }
func (p *Package) Description() string {
	return "The user's standing decisions about how work is done, one rulebook per area. List what exists and which channels load it; propose a change and the user approves it."
}

// Group is core: rules describe how to work, so every channel needs to reach
// them, the same way every channel can read and write files.
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupCoreTools }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__rule_*"},
	}
}

// RequiredSecrets: none. Rulebooks are files in the user's own memory directory.
func (p *Package) RequiredSecrets() []toolpkg.SecretSpec { return nil }

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Core tools available on every channel."},
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, ctx toolpkg.RegistrationContext) error {
	RegisterTools(handler, Deps{
		MemoryDir:    ctx.MemoryDir,
		ArmRuleWrite: p.ArmRuleWrite,
	})
	return nil
}
