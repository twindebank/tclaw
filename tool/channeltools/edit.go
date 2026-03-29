package channeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/channel"
	"tclaw/mcp"
)

const ToolChannelEdit = "channel_edit"

func channelEditDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolChannelEdit,
		Description: "Update a dynamic channel's description, tool permissions, or access control. Cannot modify static channels (from config file). The agent restarts automatically to apply changes.",
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
					"items": {"type": "integer"},
					"description": "Platform user IDs for access control. Replaces existing list."
				},
				"allowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tools this channel is allowed to use. Replaces any existing allowed_tools for this channel."
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
	AllowedUsers    *[]int64        `json:"allowed_users"`
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

		// Reject editing static channels — they are defined in the live config
		// file (gitignored, not in source control). Return a helpful error with
		// the exact file path and YAML snippet so the agent can edit it directly
		// using Read/Edit tools, then deploy to apply the change.
		if deps.Registry.IsStatic(a.Name) {
			return nil, fmt.Errorf(
				"channel %q is a static channel defined in %s.\n\n"+
					"tclaw.yaml is not in git — edit it directly using the Read/Edit tools at that path, "+
					"then run deploy to apply the change.\n\n"+
					"Add a links section under the channel's entry:\n\n"+
					"    channels:\n"+
					"      - name: %s\n"+
					"        links:\n"+
					"          - target: <target-channel>\n"+
					"            description: \"When to use this link\"",
				a.Name, deps.ConfigPath, a.Name,
			)
		}

		cfg, err := deps.Registry.DynamicStore().Get(ctx, a.Name)
		if err != nil {
			return nil, fmt.Errorf("look up channel: %w", err)
		}
		if cfg == nil {
			return nil, fmt.Errorf("dynamic channel %q not found", a.Name)
		}

		if a.Description != "" {
			if err := deps.Registry.DynamicStore().Update(ctx, a.Name, func(c *channel.DynamicChannelConfig) {
				c.Description = a.Description
			}); err != nil {
				return nil, fmt.Errorf("edit channel: %w", err)
			}
		}

		if a.AllowedTools != nil || a.DisallowedTools != nil {
			if err := deps.Registry.DynamicStore().Update(ctx, a.Name, func(c *channel.DynamicChannelConfig) {
				if a.AllowedTools != nil {
					c.AllowedTools = a.AllowedTools
				}
				if a.DisallowedTools != nil {
					c.DisallowedTools = a.DisallowedTools
				}
			}); err != nil {
				return nil, fmt.Errorf("edit channel tools: %w", err)
			}
		}

		if a.AllowedUsers != nil {
			if err := deps.Registry.DynamicStore().Update(ctx, a.Name, func(c *channel.DynamicChannelConfig) {
				c.AllowedUsers = *a.AllowedUsers

				// Update PlatformState if the channel has one and users changed.
				if len(*a.AllowedUsers) > 0 {
					if _, ok := c.PlatformState.(channel.TelegramPlatformState); ok {
						c.PlatformState = channel.TelegramPlatformState{ChatID: (*a.AllowedUsers)[0]}
					}
				}
			}); err != nil {
				return nil, fmt.Errorf("edit channel allowed_users: %w", err)
			}
		}

		// Update links if provided.
		if a.Links != nil {
			// Validate links.
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
			if err := deps.Registry.DynamicStore().Update(ctx, a.Name, func(c *channel.DynamicChannelConfig) {
				c.Links = *a.Links
			}); err != nil {
				return nil, fmt.Errorf("edit channel links: %w", err)
			}
		}

		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		result := map[string]any{
			"name":    a.Name,
			"message": fmt.Sprintf("Channel %q updated. The agent will restart automatically to apply changes.", a.Name),
		}
		if a.Description != "" {
			result["description"] = a.Description
		}
		return json.Marshal(result)
	}
}
