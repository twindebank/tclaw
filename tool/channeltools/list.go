package channeltools

import (
	"context"
	"encoding/json"

	"tclaw/mcp"
)

const ToolChannelList = "channel_list"

func channelListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolChannelList,
		Description: "List all channels. Shows name, type, description, and tool permissions.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

type channelListEntry struct {
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Description     string   `json:"description"`
	Purpose         string   `json:"purpose,omitempty"`
	Parent          string   `json:"parent,omitempty"`
	AllowedTools    []string `json:"allowed_tools,omitempty"`
	DisallowedTools []string `json:"disallowed_tools,omitempty"`
}

func channelListHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		entries := deps.Registry.All()

		var result []channelListEntry
		for _, e := range entries {
			result = append(result, channelListEntry{
				Name:            e.Name,
				Type:            string(e.Type),
				Description:     e.Description,
				Purpose:         e.Purpose,
				Parent:          e.Parent,
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
