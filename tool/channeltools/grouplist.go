package channeltools

import (
	"context"
	"encoding/json"

	"tclaw/mcp"
	"tclaw/toolgroup"
)

func toolGroupListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: "tool_group_list",
		Description: "List all available tool groups with their descriptions and the tools they contain. " +
			"Use this when creating channels to understand what permissions each group provides, " +
			"and when deciding which creatable_groups to set.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	}
}

type toolGroupEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
}

func toolGroupListHandler() mcp.ToolHandler {
	return func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		allGroups := toolgroup.AllGroups()
		entries := make([]toolGroupEntry, len(allGroups))

		for i, info := range allGroups {
			tools := toolgroup.GroupTools(info.Group)
			toolNames := make([]string, len(tools))
			for j, t := range tools {
				toolNames[j] = string(t)
			}
			entries[i] = toolGroupEntry{
				Name:        string(info.Group),
				Description: info.Description,
				Tools:       toolNames,
			}
		}

		return json.Marshal(entries)
	}
}
