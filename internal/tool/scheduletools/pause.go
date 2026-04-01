package scheduletools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/internal/mcp"
	"tclaw/internal/schedule"
)

const ToolPause = "schedule_pause"

func schedulePauseDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolPause,
		Description: "Pause an active schedule. It will stop firing until resumed.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"id": {
					"type": "string",
					"description": "The schedule ID to pause. Use schedule_list to find IDs."
				}
			},
			"required": ["id"]
		}`),
	}
}

type schedulePauseArgs struct {
	ID string `json:"id"`
}

func schedulePauseHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a schedulePauseArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.ID == "" {
			return nil, fmt.Errorf("id is required")
		}

		err := deps.Store.Update(ctx, schedule.ScheduleID(a.ID), func(sched *schedule.Schedule) {
			sched.Status = schedule.StatusPaused
		})
		if err != nil {
			return nil, fmt.Errorf("pause schedule: %w", err)
		}

		deps.Scheduler.Reload()

		return json.Marshal(fmt.Sprintf("Schedule %s paused.", a.ID))
	}
}
