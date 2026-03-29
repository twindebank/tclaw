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

func onboardingAdvanceDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolAdvance,
		Description: "Advance the onboarding phase. Call this when you've completed the current phase and are ready to move to the next one. Transitions: welcome → info_gathering → tips_active → complete.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_name": {
					"type": "string",
					"description": "Target channel for the tips schedule. Required when advancing to tips_active phase."
				}
			}
		}`),
	}
}

type advanceArgs struct {
	ChannelName string `json:"channel_name"`
}

func onboardingAdvanceHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a advanceArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		state, err := deps.Store.Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("get onboarding state: %w", err)
		}
		if state == nil {
			return nil, fmt.Errorf("onboarding has not been initialized")
		}

		switch state.Phase {
		case onboarding.PhaseWelcome:
			state.Phase = onboarding.PhaseInfoGathering

		case onboarding.PhaseInfoGathering:
			if a.ChannelName == "" {
				return nil, fmt.Errorf("channel_name is required when advancing to tips_active — specify which channel should receive daily tips")
			}

			// Create the tips schedule automatically.
			schedID, err := createTipsSchedule(ctx, deps.ScheduleStore, deps.Scheduler, a.ChannelName)
			if err != nil {
				return nil, fmt.Errorf("create tips schedule: %w", err)
			}
			state.TipsScheduleID = string(schedID)
			state.Phase = onboarding.PhaseTipsActive

		case onboarding.PhaseTipsActive:
			// Clean up the tips schedule.
			if state.TipsScheduleID != "" {
				if err := deps.ScheduleStore.Remove(ctx, schedule.ScheduleID(state.TipsScheduleID)); err != nil {
					// Non-fatal — schedule may have been manually deleted.
					slog.Warn("failed to remove tips schedule", "schedule_id", state.TipsScheduleID, "err", err)
				}
				deps.Scheduler.Reload()
			}
			state.Phase = onboarding.PhaseComplete
			state.CompletedAt = time.Now()

		case onboarding.PhaseComplete:
			return json.Marshal(map[string]string{
				"phase":   string(state.Phase),
				"message": "Onboarding is already complete.",
			})

		default:
			return nil, fmt.Errorf("unknown phase: %s", state.Phase)
		}

		if err := deps.Store.Set(ctx, state); err != nil {
			return nil, fmt.Errorf("save onboarding state: %w", err)
		}

		result := map[string]any{
			"phase":   string(state.Phase),
			"message": fmt.Sprintf("Advanced to %s phase.", state.Phase),
		}
		if state.TipsScheduleID != "" {
			result["tips_schedule_id"] = state.TipsScheduleID
		}
		return json.Marshal(result)
	}
}

// createTipsSchedule creates a daily tips schedule that fires at 10:00 AM.
func createTipsSchedule(ctx context.Context, schedStore *schedule.Store, scheduler *schedule.Scheduler, channelName string) (schedule.ScheduleID, error) {
	now := time.Now()

	// Parse the cron expression to compute the first fire time.
	cronExpr := "0 10 * * *" // 10:00 AM daily

	sched := schedule.Schedule{
		ID:          schedule.GenerateID(),
		CronExpr:    cronExpr,
		Prompt:      "[Onboarding] Time for a feature tip! Check onboarding_status to see which feature area is next. Teach the user about it naturally — tailor the tip to what you know about them from memory. After delivering, call onboarding_tip_shown with the area ID.",
		ChannelName: channelName,
		Status:      schedule.StatusActive,
		CreatedAt:   now,
	}

	if err := schedStore.Add(ctx, sched); err != nil {
		return "", err
	}
	scheduler.Reload()

	return sched.ID, nil
}
