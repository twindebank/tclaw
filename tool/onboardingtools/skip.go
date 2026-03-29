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

func onboardingSkipDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolSkip,
		Description: "Skip onboarding entirely — marks it as complete immediately. Use when the user says they don't need the guided tour or want to explore on their own.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func onboardingSkipHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		state, err := deps.Store.Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("get onboarding state: %w", err)
		}
		if state == nil {
			// Create and immediately complete.
			state = &onboarding.State{
				Phase:        onboarding.PhaseComplete,
				StartedAt:    time.Now(),
				CompletedAt:  time.Now(),
				InfoGathered: make(map[string]bool),
			}
		} else {
			// Clean up tips schedule if one exists.
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

		return json.Marshal(map[string]string{
			"phase":   string(onboarding.PhaseComplete),
			"message": "Onboarding skipped. You can always ask about features anytime.",
		})
	}
}
