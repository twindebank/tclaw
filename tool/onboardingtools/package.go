package onboardingtools

import (
	"context"
	"fmt"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/onboarding"
	"tclaw/schedule"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// ExtraKeyOnboardingStore is the RegistrationContext.Extra key for *onboarding.Store.
const ExtraKeyOnboardingStore = "onboarding_store"

// Package implements toolpkg.Package for onboarding tools.
type Package struct{}

func (p *Package) Name() string { return "onboarding" }
func (p *Package) Description() string {
	return "New user onboarding flow: track progress, deliver tips, manage setup phases."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupOnboarding }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__onboarding_*"}
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
	onboardingStore, ok := ctx.Extra[ExtraKeyOnboardingStore].(*onboarding.Store)
	if !ok || onboardingStore == nil {
		return fmt.Errorf("onboardingtools: missing %s in RegistrationContext.Extra", ExtraKeyOnboardingStore)
	}
	schedStore, ok := ctx.Extra[scheduletools_ExtraKeyScheduleStore].(*schedule.Store)
	if !ok || schedStore == nil {
		return fmt.Errorf("onboardingtools: missing %s in RegistrationContext.Extra", scheduletools_ExtraKeyScheduleStore)
	}
	scheduler, ok := ctx.Extra[scheduletools_ExtraKeyScheduler].(*schedule.Scheduler)
	if !ok || scheduler == nil {
		return fmt.Errorf("onboardingtools: missing %s in RegistrationContext.Extra", scheduletools_ExtraKeyScheduler)
	}

	RegisterTools(handler, Deps{
		Store:         onboardingStore,
		ScheduleStore: schedStore,
		Scheduler:     scheduler,
	})
	return nil
}

// Re-export schedule extra keys to avoid import cycle.
const scheduletools_ExtraKeyScheduleStore = "schedule_store"
const scheduletools_ExtraKeyScheduler = "scheduler"
