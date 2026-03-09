package channeltools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/channel"
	"tclaw/mcp"
)

func channelCreateDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "channel_create",
		Description: "Create a new dynamic channel. Only socket channels are supported. The channel becomes active after the agent restarts (send 'stop' or wait for idle timeout).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Short name for the channel (e.g. 'phone', 'tablet'). Used in socket path and message routing. Must be unique across all channels."
				},
				"description": {
					"type": "string",
					"description": "Describes the device or context (e.g. 'Mobile phone', 'Work tablet'). Helps the agent tailor responses."
				}
			},
			"required": ["name", "description"]
		}`),
	}
}

type channelCreateArgs struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func channelCreateHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelCreateArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Name == "" {
			return nil, fmt.Errorf("name is required")
		}
		if a.Description == "" {
			return nil, fmt.Errorf("description is required")
		}

		// Check uniqueness against static channels.
		for _, info := range deps.StaticChannels {
			if info.Name == a.Name {
				return nil, fmt.Errorf("channel name %q is already used by a static channel (from config file)", a.Name)
			}
		}

		cfg := channel.DynamicChannelConfig{
			Name:        a.Name,
			Type:        channel.TypeSocket,
			Description: a.Description,
			CreatedAt:   time.Now(),
		}
		if err := deps.DynamicStore.Add(ctx, cfg); err != nil {
			return nil, fmt.Errorf("create channel: %w", err)
		}

		result := map[string]any{
			"name":        cfg.Name,
			"type":        string(cfg.Type),
			"description": cfg.Description,
			"message":     fmt.Sprintf("Channel %q created. It will become active after the agent restarts — send 'stop' or wait for idle timeout.", a.Name),
		}
		return json.Marshal(result)
	}
}
