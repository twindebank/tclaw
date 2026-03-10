package scheduletools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"

	"tclaw/mcp"
	"tclaw/schedule"
)

func scheduleResumeDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "schedule_resume",
		Description: "Resume a paused schedule. It will start firing again on its cron expression.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"id": {
					"type": "string",
					"description": "The schedule ID to resume. Use schedule_list to find IDs."
				}
			},
			"required": ["id"]
		}`),
	}
}

type scheduleResumeArgs struct {
	ID string `json:"id"`
}

func scheduleResumeHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a scheduleResumeArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.ID == "" {
			return nil, fmt.Errorf("id is required")
		}

		err := deps.Store.Update(ctx, schedule.ScheduleID(a.ID), func(sched *schedule.Schedule) {
			sched.Status = schedule.StatusActive
			// Recalculate next run time from now.
			parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
			if cronSched, parseErr := parser.Parse(sched.CronExpr); parseErr == nil {
				sched.NextRunAt = cronSched.Next(time.Now())
			}
		})
		if err != nil {
			return nil, fmt.Errorf("resume schedule: %w", err)
		}

		deps.Scheduler.Reload()

		return json.Marshal(fmt.Sprintf("Schedule %s resumed.", a.ID))
	}
}
