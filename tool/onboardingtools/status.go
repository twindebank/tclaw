package onboardingtools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/mcp"
	"tclaw/onboarding"
)

func onboardingStatusDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "onboarding_status",
		Description: "Get the current onboarding state — phase, info gathered, tips shown, etc. Use this to understand where the user is in the onboarding journey.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func onboardingStatusHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		state, err := deps.Store.Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("get onboarding state: %w", err)
		}
		if state == nil {
			return json.Marshal(map[string]string{
				"phase":   string(onboarding.PhaseWelcome),
				"message": "No onboarding state exists yet — this is a brand new user.",
			})
		}

		nextTip := onboarding.NextArea(state.TipsShown)
		var nextTipID string
		if nextTip != nil {
			nextTipID = nextTip.ID
		}

		result := map[string]any{
			"phase":             string(state.Phase),
			"started_at":        state.StartedAt,
			"info_gathered":     state.InfoGathered,
			"tips_shown":        state.TipsShown,
			"tips_remaining":    len(onboarding.FeatureAreas) - len(state.TipsShown),
			"next_tip":          nextTipID,
			"tips_schedule_id":  state.TipsScheduleID,
		}
		if !state.CompletedAt.IsZero() {
			result["completed_at"] = state.CompletedAt
		}
		return json.Marshal(result)
	}
}
