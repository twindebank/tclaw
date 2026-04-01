package onboardingtools

import (
	"tclaw/internal/mcp"
	"tclaw/internal/onboarding"
	"tclaw/internal/schedule"
)

const (
	ToolStatus   = "onboarding_status"
	ToolSetInfo  = "onboarding_set_info"
	ToolAdvance  = "onboarding_advance"
	ToolTipShown = "onboarding_tip_shown"
	ToolSkip     = "onboarding_skip"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{ToolStatus, ToolSetInfo, ToolAdvance, ToolTipShown, ToolSkip}
}

// Deps holds dependencies for onboarding tools.
type Deps struct {
	Store         *onboarding.Store
	ScheduleStore *schedule.Store
	Scheduler     *schedule.Scheduler
}

// RegisterTools adds onboarding management tools to the MCP handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(onboardingStatusDef(), onboardingStatusHandler(deps))
	handler.Register(onboardingSetInfoDef(), onboardingSetInfoHandler(deps))
	handler.Register(onboardingAdvanceDef(), onboardingAdvanceHandler(deps))
	handler.Register(onboardingTipShownDef(), onboardingTipShownHandler(deps))
	handler.Register(onboardingSkipDef(), onboardingSkipHandler(deps))
}
