package scheduletools

import (
	"context"
	"fmt"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/schedule"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// ExtraKeyScheduleStore is the RegistrationContext.Extra key for *schedule.Store.
const ExtraKeyScheduleStore = "schedule_store"

// ExtraKeyScheduler is the RegistrationContext.Extra key for *schedule.Scheduler.
const ExtraKeyScheduler = "scheduler"

// Package implements toolpkg.Package for cron schedule management tools.
type Package struct{}

func (p *Package) Name() string { return "schedule" }
func (p *Package) Description() string {
	return "Create, edit, delete, pause, and resume cron schedules that fire prompts on channels at specified times."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupScheduling }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__schedule_*"}
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
	schedStore, ok := ctx.Extra[ExtraKeyScheduleStore].(*schedule.Store)
	if !ok || schedStore == nil {
		return fmt.Errorf("scheduletools: missing %s in RegistrationContext.Extra", ExtraKeyScheduleStore)
	}
	scheduler, ok := ctx.Extra[ExtraKeyScheduler].(*schedule.Scheduler)
	if !ok || scheduler == nil {
		return fmt.Errorf("scheduletools: missing %s in RegistrationContext.Extra", ExtraKeyScheduler)
	}

	RegisterTools(handler, Deps{
		Store:     schedStore,
		Scheduler: scheduler,
	})
	return nil
}
