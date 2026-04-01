package notificationtools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/internal/mcp"
)

func listDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolList,
		Description: "List all active notification subscriptions with their ID, package, type, channel, scope, label, and creation time.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

type listEntry struct {
	ID              string `json:"id"`
	PackageName     string `json:"package_name"`
	TypeName        string `json:"type_name"`
	ChannelName     string `json:"channel_name"`
	Scope           string `json:"scope"`
	CredentialSetID string `json:"credential_set_id,omitempty"`
	Label           string `json:"label"`
	CreatedAt       string `json:"created_at"`
}

func listHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		subs, err := deps.Manager.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("list subscriptions: %w", err)
		}

		if len(subs) == 0 {
			return json.Marshal("No active notification subscriptions. Use notification_types to see what's available, then notification_subscribe to start watching.")
		}

		entries := make([]listEntry, 0, len(subs))
		for _, sub := range subs {
			entries = append(entries, listEntry{
				ID:              string(sub.ID),
				PackageName:     sub.PackageName,
				TypeName:        sub.TypeName,
				ChannelName:     sub.ChannelName,
				Scope:           string(sub.Scope),
				CredentialSetID: sub.CredentialSetID,
				Label:           sub.Label,
				CreatedAt:       sub.CreatedAt.Format(time.RFC3339),
			})
		}

		return json.Marshal(entries)
	}
}
