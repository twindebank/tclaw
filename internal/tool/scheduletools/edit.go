package scheduletools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"tclaw/internal/mcp"
	"tclaw/internal/schedule"
)

const ToolEdit = "schedule_edit"

func scheduleEditDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolEdit,
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
				},
				"timezone": {
					"type": "string",
					"description": "New IANA timezone for evaluating the cron expression (e.g. 'Europe/London'). Pass empty string to reset to system default (optional)."
				}
			},
			"required": ["id"]
		}`),
	}
}

type scheduleEditArgs struct {
	ID          string  `json:"id"`
	Prompt      string  `json:"prompt"`
	CronExpr    string  `json:"cron_expr"`
	ChannelName string  `json:"channel_name"`
	// Timezone uses a pointer so we can distinguish "not provided" (nil) from "reset to
	// system default" (pointer to empty string). Other string fields use empty-means-unchanged.
	Timezone    *string `json:"timezone"`
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

		// Validate timezone if provided (non-nil, non-empty means a new IANA name).
		if a.Timezone != nil && *a.Timezone != "" {
			if _, err := time.LoadLocation(*a.Timezone); err != nil {
				return nil, fmt.Errorf("invalid timezone %q: %w. Use an IANA timezone name, e.g. 'Europe/London', 'America/New_York'", *a.Timezone, err)
			}
		}

		scheduleID := schedule.ScheduleID(a.ID)
		err := deps.Store.Update(ctx, scheduleID, func(sched *schedule.Schedule) {
			if a.Prompt != "" {
				sched.Prompt = a.Prompt
			}
			if a.Timezone != nil {
				sched.Timezone = *a.Timezone
			}
			if a.CronExpr != "" {
				sched.CronExpr = a.CronExpr
			}
			// Recalculate NextRunAt if the cron expression or timezone changed, since
			// either affects when the schedule next fires.
			if a.CronExpr != "" || a.Timezone != nil {
				// Use the resolved effective timezone (post-edit value).
				loc := time.Local
				if sched.Timezone != "" {
					var locErr error
					loc, locErr = time.LoadLocation(sched.Timezone)
					if locErr != nil {
						// Already validated above — this should not happen.
						slog.Error("timezone parse failed after validation", "timezone", sched.Timezone, "err", locErr)
						loc = time.Local
					}
				}
				parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
				cronSched, parseErr := parser.Parse(sched.CronExpr)
				if parseErr != nil {
					// Already validated above — this should not happen.
					slog.Error("cron parse failed after validation", "cron", sched.CronExpr, "err", parseErr)
				} else {
					sched.NextRunAt = cronSched.Next(time.Now().In(loc))
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
