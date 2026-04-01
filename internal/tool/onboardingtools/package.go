package onboardingtools

import (
	"context"

	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/onboarding"
	"tclaw/internal/schedule"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

// Package implements toolpkg.Package for onboarding tools.
type Package struct {
	Store         *onboarding.Store
	ScheduleStore *schedule.Store
	Scheduler     *schedule.Scheduler
}

func (p *Package) Name() string { return "onboarding" }
func (p *Package) Description() string {
	return "New user onboarding flow: track progress, deliver tips, manage setup phases."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupOnboarding }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__onboarding_*"},
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec { return nil }

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "New user onboarding flow: track progress, deliver tips, manage setup phases."},
		Credentials: nil,
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, ctx toolpkg.RegistrationContext) error {
	RegisterTools(handler, Deps{
		Store:         p.Store,
		ScheduleStore: p.ScheduleStore,
		Scheduler:     p.Scheduler,
	})
	return nil
}
