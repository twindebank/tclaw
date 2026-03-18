package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/channel"
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
	AllowedUsers    []int64   `json:"allowed_users,omitempty"`
}

func channelListHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		var entries []channelListEntry

		// Static channels from config.
		for _, info := range deps.StaticChannels {
			entries = append(entries, channelListEntry{
				Name:            info.Name,
				Type:            string(info.Type),
				Description:     info.Description,
				Source:          string(channel.SourceStatic),
				Role:            info.Role,
				AllowedTools:    info.AllowedTools,
				DisallowedTools: info.DisallowedTools,
			})
		}

		// Dynamic channels from user store.
		dynamics, err := deps.DynamicStore.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("list dynamic channels: %w", err)
		}
		for _, cfg := range dynamics {
			entries = append(entries, channelListEntry{
				Name:            cfg.Name,
				Type:            string(cfg.Type),
				Description:     cfg.Description,
				Source:          string(channel.SourceDynamic),
				Role:            cfg.Role,
				AllowedTools:    cfg.AllowedTools,
				DisallowedTools: cfg.DisallowedTools,
				AllowedUsers:    cfg.AllowedUsers,
			})
		}

		if len(entries) == 0 {
			return json.Marshal([]channelListEntry{})
		}

		return json.Marshal(entries)
	}
}
