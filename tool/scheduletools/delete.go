package scheduletools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/mcp"
	"tclaw/schedule"
)

func scheduleDeleteDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "schedule_delete",
		Description: "Delete a schedule permanently.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"id": {
					"type": "string",
					"description": "The schedule ID to delete. Use schedule_list to find IDs."
				}
			},
			"required": ["id"]
		}`),
	}
}

type scheduleDeleteArgs struct {
	ID string `json:"id"`
}

func scheduleDeleteHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a scheduleDeleteArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.ID == "" {
			return nil, fmt.Errorf("id is required")
		}

		if err := deps.Store.Remove(ctx, schedule.ScheduleID(a.ID)); err != nil {
			return nil, fmt.Errorf("delete schedule: %w", err)
		}

		deps.Scheduler.Reload()

		return json.Marshal(fmt.Sprintf("Schedule %s deleted.", a.ID))
	}
}
