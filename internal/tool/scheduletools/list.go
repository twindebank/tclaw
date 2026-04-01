package scheduletools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/internal/mcp"
)

const ToolList = "schedule_list"

func scheduleListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolList,
		Description: "List all scheduled prompts with their ID, cron expression, full prompt text, channel, status, and next/last run times.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

type scheduleListEntry struct {
	ID          string `json:"id"`
	CronExpr    string `json:"cron_expr"`
	Prompt      string `json:"prompt"`
	ChannelName string `json:"channel_name"`
	Status      string `json:"status"`
	LastRunAt   string `json:"last_run_at,omitempty"`
	NextRunAt   string `json:"next_run_at,omitempty"`
}

func scheduleListHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		schedules, err := deps.Store.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("list schedules: %w", err)
		}

		if len(schedules) == 0 {
			return json.Marshal("No schedules configured.")
		}

		entries := make([]scheduleListEntry, 0, len(schedules))
		for _, sched := range schedules {
			entry := scheduleListEntry{
				ID:          string(sched.ID),
				CronExpr:    sched.CronExpr,
				Prompt:      sched.Prompt,
				ChannelName: sched.ChannelName,
				Status:      string(sched.Status),
			}
			if !sched.LastRunAt.IsZero() {
				entry.LastRunAt = sched.LastRunAt.Format(time.RFC3339)
			}
			if !sched.NextRunAt.IsZero() {
				entry.NextRunAt = sched.NextRunAt.Format(time.RFC3339)
			}
			entries = append(entries, entry)
		}

		return json.Marshal(entries)
	}
}
