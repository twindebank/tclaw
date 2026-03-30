package scheduletools

import (
	"context"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/schedule"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// Package implements toolpkg.Package for cron schedule management tools.
type Package struct {
	Store     *schedule.Store
	Scheduler *schedule.Scheduler
}

func (p *Package) Name() string { return "schedule" }
func (p *Package) Description() string {
	return "Create, edit, delete, pause, and resume cron schedules that fire prompts on channels at specified times."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupScheduling }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__schedule_*"},
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec { return nil }

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Create, edit, delete, pause, and resume cron schedules."},
		Credentials: nil,
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, ctx toolpkg.RegistrationContext) error {
	RegisterTools(handler, Deps{
		Store:     p.Store,
		Scheduler: p.Scheduler,
	})
	return nil
}
