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

const ToolCreate = "schedule_create"

func scheduleCreateDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolCreate,
		Description: "Create a new scheduled prompt that fires on a cron schedule. The prompt is injected into the target channel as if the user sent it. " +
			"Accepts 5-field cron (minute hour dom month dow) or shortcuts (@daily, @hourly, @weekly, @every 12h). " +
			"Examples: '0 9,18 * * *' (9am and 6pm), '0 8 * * 1-5' (weekday mornings), '*/30 * * * *' (every 30 min). " +
			"Confirm the schedule timing with the user before creating.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"prompt": {
					"type": "string",
					"description": "The message text to inject when the schedule fires."
				},
				"cron_expr": {
					"type": "string",
					"description": "Cron expression (5-field: minute hour day-of-month month day-of-week) or descriptor (@daily, @hourly, @weekly, @every 12h). Examples: '0 9,18 * * *' (9am and 6pm), '0 8 * * 1-5' (weekday mornings), '*/30 * * * *' (every 30 min)."
				},
				"channel_name": {
					"type": "string",
					"description": "Target channel name. If omitted, defaults to the channel from the current message context."
				},
				"timezone": {
					"type": "string",
					"description": "IANA timezone for evaluating the cron expression (e.g. 'Europe/London', 'America/New_York'). If omitted, uses the server's system timezone."
				}
			},
			"required": ["prompt", "cron_expr"]
		}`),
	}
}

type scheduleCreateArgs struct {
	Prompt      string `json:"prompt"`
	CronExpr    string `json:"cron_expr"`
	ChannelName string `json:"channel_name"`
	Timezone    string `json:"timezone"`
}

func scheduleCreateHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a scheduleCreateArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Prompt == "" {
			return nil, fmt.Errorf("prompt is required")
		}
		if a.CronExpr == "" {
			return nil, fmt.Errorf("cron_expr is required")
		}

		// Validate the cron expression.
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		cronSched, err := parser.Parse(a.CronExpr)
		if err != nil {
			return nil, fmt.Errorf("invalid cron expression %q: %w. Examples: '0 9 * * *' (daily 9am), '*/30 * * * *' (every 30 min), '@hourly', '@daily'", a.CronExpr, err)
		}

		// Validate the timezone if provided, and use it when computing NextRunAt so
		// that the stored time reflects when the schedule will actually first fire.
		loc := time.Local
		if a.Timezone != "" {
			loc, err = time.LoadLocation(a.Timezone)
			if err != nil {
				return nil, fmt.Errorf("invalid timezone %q: %w. Use an IANA timezone name, e.g. 'Europe/London', 'America/New_York'", a.Timezone, err)
			}
		}

		if a.ChannelName == "" {
			return nil, fmt.Errorf("channel_name is required — specify which channel to send the prompt to")
		}

		now := time.Now()
		sched := schedule.Schedule{
			ID:          schedule.GenerateID(),
			CronExpr:    a.CronExpr,
			Prompt:      a.Prompt,
			ChannelName: a.ChannelName,
			Timezone:    a.Timezone,
			Status:      schedule.StatusActive,
			CreatedAt:   now,
			NextRunAt:   cronSched.Next(now.In(loc)),
		}

		if err := deps.Store.Add(ctx, sched); err != nil {
			return nil, fmt.Errorf("create schedule: %w", err)
		}

		deps.Scheduler.Reload()

		result := map[string]any{
			"id":           string(sched.ID),
			"cron_expr":    sched.CronExpr,
			"prompt":       sched.Prompt,
			"channel_name": sched.ChannelName,
			"timezone":     sched.Timezone,
			"status":       string(sched.Status),
			"next_run_at":  sched.NextRunAt.Format(time.RFC3339),
			"message":      fmt.Sprintf("Schedule %s created. Next fire: %s", sched.ID, sched.NextRunAt.Format("Mon Jan 2 15:04")),
		}
		return json.Marshal(result)
	}
}
