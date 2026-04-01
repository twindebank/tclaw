package scheduletools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"

	"tclaw/internal/mcp"
	"tclaw/internal/schedule"
)

const ToolResume = "schedule_resume"

func scheduleResumeDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolResume,
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

		var cronParseErr error
		err := deps.Store.Update(ctx, schedule.ScheduleID(a.ID), func(sched *schedule.Schedule) {
			sched.Status = schedule.StatusActive
			parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
			cronSched, parseErr := parser.Parse(sched.CronExpr)
			if parseErr != nil {
				// Can't return from inside the update func — capture for the caller.
				cronParseErr = parseErr
				return
			}
			sched.NextRunAt = cronSched.Next(time.Now())
		})
		if err != nil {
			return nil, fmt.Errorf("resume schedule: %w", err)
		}
		if cronParseErr != nil {
			return nil, fmt.Errorf("schedule has invalid cron expression: %w", cronParseErr)
		}

		deps.Scheduler.Reload()

		return json.Marshal(fmt.Sprintf("Schedule %s resumed.", a.ID))
	}
}
