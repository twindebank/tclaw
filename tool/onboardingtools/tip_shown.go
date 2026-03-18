package onboardingtools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"tclaw/mcp"
	"tclaw/onboarding"
	"tclaw/schedule"
)

func onboardingTipShownDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "onboarding_tip_shown",
		Description: "Record that an onboarding tip was delivered to the user. Call this after you've presented a tip so it won't be repeated. If this was the last tip, the tips schedule is automatically cleaned up and onboarding completes.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"tip_id": {
					"type": "string",
					"description": "The ID of the tip that was shown (e.g. 'memory', 'connections', 'scheduling')."
				}
			},
			"required": ["tip_id"]
		}`),
	}
}

type tipShownArgs struct {
	TipID string `json:"tip_id"`
}

func onboardingTipShownHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a tipShownArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.TipID == "" {
			return nil, fmt.Errorf("tip_id is required")
		}

		state, err := deps.Store.Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("get onboarding state: %w", err)
		}
		if state == nil {
			return nil, fmt.Errorf("onboarding has not been initialized")
		}

		// Deduplicate — don't record the same tip twice.
		for _, shown := range state.TipsShown {
			if shown == a.TipID {
				return json.Marshal(map[string]string{
					"message": fmt.Sprintf("Tip %q was already recorded.", a.TipID),
				})
			}
		}

		state.TipsShown = append(state.TipsShown, a.TipID)

		// Check if all tips have been shown.
		nextTip := onboarding.NextArea(state.TipsShown)
		allDone := nextTip == nil

		if allDone {
			// Auto-complete: remove the schedule and advance to complete.
			if state.TipsScheduleID != "" {
				if removeErr := deps.ScheduleStore.Remove(ctx, schedule.ScheduleID(state.TipsScheduleID)); removeErr != nil {
					// Non-fatal.
					slog.Warn("failed to remove tips schedule", "schedule_id", state.TipsScheduleID, "err", removeErr)
				}
				deps.Scheduler.Reload()
			}
			state.Phase = onboarding.PhaseComplete
			state.CompletedAt = time.Now()
		}

		if err := deps.Store.Set(ctx, state); err != nil {
			return nil, fmt.Errorf("save onboarding state: %w", err)
		}

		result := map[string]any{
			"recorded":       a.TipID,
			"tips_remaining": len(onboarding.FeatureAreas) - len(state.TipsShown),
			"all_tips_done":  allDone,
		}
		if allDone {
			result["message"] = "All tips delivered! Onboarding is complete."
			result["phase"] = string(onboarding.PhaseComplete)
		} else {
			result["next_tip"] = nextTip.ID
			result["message"] = fmt.Sprintf("Recorded tip %q. %d tips remaining, next: %s", a.TipID, len(onboarding.FeatureAreas)-len(state.TipsShown), nextTip.ID)
		}
		return json.Marshal(result)
	}
}
