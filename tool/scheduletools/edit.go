package scheduletools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"tclaw/mcp"
	"tclaw/schedule"
)

func scheduleEditDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "schedule_edit",
		Description: "Edit an existing schedule. Only provided fields are updated — omit fields to leave them unchanged.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"id": {
					"type": "string",
					"description": "The schedule ID to edit. Use schedule_list to find IDs."
				},
				"prompt": {
					"type": "string",
					"description": "New prompt text (optional)."
				},
				"cron_expr": {
					"type": "string",
					"description": "New cron expression (optional)."
				},
				"channel_name": {
					"type": "string",
					"description": "New target channel name (optional)."
				}
			},
			"required": ["id"]
		}`),
	}
}

type scheduleEditArgs struct {
	ID          string `json:"id"`
	Prompt      string `json:"prompt"`
	CronExpr    string `json:"cron_expr"`
	ChannelName string `json:"channel_name"`
}

func scheduleEditHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a scheduleEditArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.ID == "" {
			return nil, fmt.Errorf("id is required")
		}

		// Validate cron if provided.
		if a.CronExpr != "" {
			parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
			if _, err := parser.Parse(a.CronExpr); err != nil {
				return nil, fmt.Errorf("invalid cron expression %q: %w", a.CronExpr, err)
			}
		}

		scheduleID := schedule.ScheduleID(a.ID)
		err := deps.Store.Update(ctx, scheduleID, func(sched *schedule.Schedule) {
			if a.Prompt != "" {
				sched.Prompt = a.Prompt
			}
			if a.CronExpr != "" {
				sched.CronExpr = a.CronExpr
				// Recalculate next run time. The cron was already validated above,
				// so this parse should not fail — but surface it if it does.
				parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
				cronSched, parseErr := parser.Parse(a.CronExpr)
				if parseErr != nil {
					slog.Error("cron parse failed after validation", "cron", a.CronExpr, "err", parseErr)
				} else {
					sched.NextRunAt = cronSched.Next(time.Now())
				}
			}
			if a.ChannelName != "" {
				sched.ChannelName = a.ChannelName
			}
		})
		if err != nil {
			return nil, fmt.Errorf("edit schedule: %w", err)
		}

		deps.Scheduler.Reload()

		result := map[string]any{
			"id":      a.ID,
			"message": fmt.Sprintf("Schedule %s updated.", a.ID),
		}
		return json.Marshal(result)
	}
}
