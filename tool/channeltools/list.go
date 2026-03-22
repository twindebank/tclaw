package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/mcp"
	"tclaw/role"
)

func channelListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "channel_list",
		Description: "List all channels (both static from config and dynamic user-created ones). Shows name, type, description, and source.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

type channelListEntry struct {
	Name            string    `json:"name"`
	Type            string    `json:"type"`
	Description     string    `json:"description"`
	Source          string    `json:"source"`
	Role            role.Role `json:"role,omitempty"`
	AllowedTools    []string  `json:"allowed_tools,omitempty"`
	DisallowedTools []string  `json:"disallowed_tools,omitempty"`
}

func channelListHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		entries, err := deps.Registry.All(ctx)
		if err != nil {
			return nil, fmt.Errorf("list channels: %w", err)
		}

		var result []channelListEntry
		for _, e := range entries {
			result = append(result, channelListEntry{
				Name:            e.Name,
				Type:            string(e.Type),
				Description:     e.Description,
				Source:          string(e.Source),
				Role:            e.Role,
				AllowedTools:    e.AllowedTools,
				DisallowedTools: e.DisallowedTools,
			})
		}

		if len(result) == 0 {
			return json.Marshal([]channelListEntry{})
		}
		return json.Marshal(result)
	}
}
