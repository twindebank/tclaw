package notificationtools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/mcp"
	"tclaw/notification"
)

func subscribeDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolSubscribe,
		Description: "Subscribe to a notification type. The tool package handles the mechanism (polling, webhook, etc.) — " +
			"you just specify the package, type, target channel, and any required parameters. " +
			"Use notification_types to see what's available.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"package_name": {
					"type": "string",
					"description": "Tool package name (e.g. 'google', 'tfl'). Use notification_types to see available packages."
				},
				"type": {
					"type": "string",
					"description": "Notification type name (e.g. 'new_email', 'disruption'). Use notification_types to see available types."
				},
				"channel_name": {
					"type": "string",
					"description": "Target channel for notifications."
				},
				"scope": {
					"type": "string",
					"enum": ["one_shot", "credential", "persistent"],
					"description": "Subscription lifetime. one_shot: auto-removed after first delivery. credential: tied to a credential set, removed when credentials are removed. persistent: lives until explicitly unsubscribed."
				},
				"credential_set_id": {
					"type": "string",
					"description": "Credential set ID (e.g. 'google/work'). Required when scope is 'credential'."
				},
				"params": {
					"type": "object",
					"description": "Type-specific parameters. Check notification_types for what each type requires.",
					"additionalProperties": { "type": "string" }
				},
				"label": {
					"type": "string",
					"description": "Human-readable label for this subscription. Auto-generated if omitted."
				}
			},
			"required": ["package_name", "type", "channel_name", "scope"]
		}`),
	}
}

type subscribeArgs struct {
	PackageName     string            `json:"package_name"`
	Type            string            `json:"type"`
	ChannelName     string            `json:"channel_name"`
	Scope           string            `json:"scope"`
	CredentialSetID string            `json:"credential_set_id"`
	Params          map[string]string `json:"params"`
	Label           string            `json:"label"`
}

func subscribeHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a subscribeArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.PackageName == "" {
			return nil, fmt.Errorf("package_name is required")
		}
		if a.Type == "" {
			return nil, fmt.Errorf("type is required")
		}
		if a.ChannelName == "" {
			return nil, fmt.Errorf("channel_name is required")
		}

		scope := notification.Scope(a.Scope)
		switch scope {
		case notification.ScopeOneShot, notification.ScopeCredential, notification.ScopePersistent:
		default:
			return nil, fmt.Errorf("invalid scope %q — must be one_shot, credential, or persistent", a.Scope)
		}

		if scope == notification.ScopeCredential && a.CredentialSetID == "" {
			return nil, fmt.Errorf("credential_set_id is required when scope is 'credential'")
		}

		label := a.Label
		if label == "" {
			label = fmt.Sprintf("%s/%s", a.PackageName, a.Type)
		}

		result, err := deps.Manager.Subscribe(ctx, a.PackageName, notification.SubscribeParams{
			TypeName:        a.Type,
			ChannelName:     a.ChannelName,
			Scope:           scope,
			CredentialSetID: a.CredentialSetID,
			Label:           label,
			Params:          a.Params,
		})
		if err != nil {
			return nil, fmt.Errorf("subscribe: %w", err)
		}

		response := map[string]any{
			"subscription_id": string(result.Subscription.ID),
			"label":           result.Subscription.Label,
			"scope":           string(result.Subscription.Scope),
			"channel":         result.Subscription.ChannelName,
			"created_at":      result.Subscription.CreatedAt.Format(time.RFC3339),
			"message":         fmt.Sprintf("Subscribed to %s/%s on channel %q", a.PackageName, a.Type, a.ChannelName),
		}
		if len(result.Info) > 0 {
			response["info"] = result.Info
		}

		return json.Marshal(response)
	}
}
