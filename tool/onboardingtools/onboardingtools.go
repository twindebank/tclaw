package onboardingtools

import (
	"tclaw/mcp"
	"tclaw/onboarding"
	"tclaw/schedule"
)

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
