package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/internal/channel"
	"tclaw/internal/config"
	"tclaw/internal/mcp"
	"tclaw/internal/toolgroup"
)

const ToolChannelEdit = "channel_edit"

func channelEditDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolChannelEdit,
		Description: "Update a channel's description, tool permissions, or access control. The agent restarts automatically to apply changes.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "The name of the channel to edit. Use channel_list to see available channels."
				},
				"description": {
					"type": "string",
					"description": "New description for the channel."
				},
				"purpose": {
					"type": "string",
					"description": "Behavioral guidance for the agent on this channel. Pass empty string to clear."
				},
				"allowed_users": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Platform user IDs for access control. Replaces existing list."
				},
				"tool_groups": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Named tool groups (additive). Replaces existing tool_groups. Mutually exclusive with allowed_tools. Use tool_group_list to see available groups."
				},
				"allowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tools this channel is allowed to use. Replaces any existing allowed_tools. Mutually exclusive with tool_groups."
				},
				"disallowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tools explicitly denied on this channel. Replaces any existing disallowed_tools."
				},
				"creatable_groups": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tool groups this channel can delegate when creating new channels. Replaces existing creatable_groups. Empty array removes channel creation ability."
				},
				"links": {
					"type": "array",
					"description": "Cross-channel messaging links. Replaces all existing links. Pass empty array to remove all links.",
					"items": {
						"type": "object",
						"properties": {
							"target": {"type": "string", "description": "Name of the target channel."},
							"description": {"type": "string", "description": "When this link should be used."}
						},
						"required": ["target", "description"]
					}
				},
				"parent": {
					"type": "string",
					"description": "Parent channel name. Pass empty string to clear."
				}
			},
			"required": ["name"]
		}`),
	}
}

type channelEditArgs struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Purpose         *string         `json:"purpose"`
	AllowedUsers    *[]string       `json:"allowed_users"`
	ToolGroups      []string        `json:"tool_groups"`
	AllowedTools    []string        `json:"allowed_tools"`
	DisallowedTools []string        `json:"disallowed_tools"`
	CreatableGroups *[]string       `json:"creatable_groups"`
	Links           *[]channel.Link `json:"links"`
	Parent          *string         `json:"parent"`
}

func channelEditHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelEditArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		hasChange := a.Description != "" || a.Purpose != nil || a.AllowedUsers != nil ||
			a.ToolGroups != nil || a.AllowedTools != nil || a.DisallowedTools != nil ||
			a.CreatableGroups != nil || a.Links != nil || a.Parent != nil
		if !hasChange {
			return nil, fmt.Errorf("at least one field to update must be provided")
		}

		if !deps.Registry.NameExists(a.Name) {
			return nil, fmt.Errorf("channel %q not found", a.Name)
		}

		// Validate tool_groups / allowed_tools mutual exclusion.
		if len(a.ToolGroups) > 0 && len(a.AllowedTools) > 0 {
			return nil, fmt.Errorf("tool_groups and allowed_tools are mutually exclusive — set exactly one")
		}

		// Validate tool group names.
		var toolGroups []toolgroup.ToolGroup
		for _, g := range a.ToolGroups {
			tg := toolgroup.ToolGroup(g)
			if !toolgroup.ValidGroup(tg) {
				return nil, fmt.Errorf("unknown tool group %q — use tool_group_list to see available groups", g)
			}
			toolGroups = append(toolGroups, tg)
		}

		// Validate creatable group names.
		if a.CreatableGroups != nil {
			for _, g := range *a.CreatableGroups {
				if !toolgroup.ValidGroup(toolgroup.ToolGroup(g)) {
					return nil, fmt.Errorf("unknown creatable group %q", g)
				}
			}
		}

		// Validate parent if explicitly set (non-empty means it must exist).
		if a.Parent != nil && *a.Parent != "" && !deps.Registry.NameExists(*a.Parent) {
			return nil, fmt.Errorf("parent channel %q not found", *a.Parent)
		}

		// Validate links before writing.
		if a.Links != nil {
			linkTargets := make(map[string]bool, len(*a.Links))
			for i, link := range *a.Links {
				if link.Target == "" || link.Description == "" {
					return nil, fmt.Errorf("links[%d]: target and description are required", i)
				}
				if link.Target == a.Name {
					return nil, fmt.Errorf("links[%d]: self-links are not allowed", i)
				}
				if linkTargets[link.Target] {
					return nil, fmt.Errorf("links[%d]: duplicate target %q", i, link.Target)
				}
				linkTargets[link.Target] = true
			}
		}

		if err := deps.ConfigWriter.UpdateChannel(deps.UserID, a.Name, func(ch *config.Channel) {
			if a.Description != "" {
				ch.Description = a.Description
			}
			if a.Purpose != nil {
				ch.Purpose = *a.Purpose
			}
			if len(toolGroups) > 0 {
				ch.ToolGroups = toolGroups
				// Clear allowed_tools since they're mutually exclusive.
				ch.AllowedTools = nil
			}
			if a.AllowedTools != nil {
				ch.AllowedTools = a.AllowedTools
				// Clear tool_groups since they're mutually exclusive.
				ch.ToolGroups = nil
			}
			if a.DisallowedTools != nil {
				ch.DisallowedTools = a.DisallowedTools
			}
			if a.CreatableGroups != nil {
				ch.CreatableGroups = creatableGroupsToToolGroups(*a.CreatableGroups)
			}
			if a.Links != nil {
				ch.Links = *a.Links
			}
			if a.Parent != nil {
				ch.Parent = *a.Parent
			}
		}); err != nil {
			return nil, fmt.Errorf("edit channel: %w", err)
		}

		// Trigger restart — the reconciler will update runtime state as needed.
		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		result := map[string]any{
			"name":    a.Name,
			"message": fmt.Sprintf("Channel %q updated. The agent will restart automatically.", a.Name),
		}
		if a.Description != "" {
			result["description"] = a.Description
		}
		return json.Marshal(result)
	}
}
