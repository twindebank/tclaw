package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/channel"
	"tclaw/config"
	"tclaw/mcp"
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
				"allowed_users": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Platform user IDs for access control. Replaces existing list."
				},
				"allowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tools this channel is allowed to use. Replaces any existing allowed_tools."
				},
				"disallowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tools explicitly denied on this channel. Replaces any existing disallowed_tools."
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
				}
			},
			"required": ["name"]
		}`),
	}
}

type channelEditArgs struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	AllowedUsers    *[]string       `json:"allowed_users"`
	AllowedTools    []string        `json:"allowed_tools"`
	DisallowedTools []string        `json:"disallowed_tools"`
	Links           *[]channel.Link `json:"links"`
}

func channelEditHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelEditArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Description == "" && a.AllowedUsers == nil && a.AllowedTools == nil && a.DisallowedTools == nil && a.Links == nil {
			return nil, fmt.Errorf("at least one of 'description', 'allowed_users', 'allowed_tools', 'disallowed_tools', or 'links' must be provided")
		}

		if !deps.Registry.NameExists(a.Name) {
			return nil, fmt.Errorf("channel %q not found", a.Name)
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
			if a.AllowedTools != nil {
				ch.AllowedTools = a.AllowedTools
			}
			if a.DisallowedTools != nil {
				ch.DisallowedTools = a.DisallowedTools
			}
			if a.Links != nil {
				ch.Links = *a.Links
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
