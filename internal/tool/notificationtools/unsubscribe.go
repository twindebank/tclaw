package notificationtools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/internal/mcp"
	"tclaw/internal/notification"
)

func unsubscribeDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolUnsubscribe,
		Description: "Unsubscribe from a notification by subscription ID. Stops the watcher and removes the subscription. Use notification_list to find subscription IDs.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"subscription_id": {
					"type": "string",
					"description": "The subscription ID to remove."
				}
			},
			"required": ["subscription_id"]
		}`),
	}
}

type unsubscribeArgs struct {
	SubscriptionID string `json:"subscription_id"`
}

func unsubscribeHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a unsubscribeArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.SubscriptionID == "" {
			return nil, fmt.Errorf("subscription_id is required")
		}

		if err := deps.Manager.Unsubscribe(ctx, notification.SubscriptionID(a.SubscriptionID)); err != nil {
			return nil, fmt.Errorf("unsubscribe: %w", err)
		}

		return json.Marshal(map[string]string{
			"message": fmt.Sprintf("Unsubscribed %s", a.SubscriptionID),
		})
	}
}
