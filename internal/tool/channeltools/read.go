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

const ToolChannelRead = "channel_read"

func channelReadDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolChannelRead,
		Description: "Return the full config for a single channel — every field that's set in tclaw.yaml. " +
			"Use this to see fields channel_list omits (model, max_turns, claude_session_timeout, " +
			"ephemeral settings, initial_message, tool groups, links, created_at).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Channel name. Use channel_list to discover available names."
				}
			},
			"required": ["name"]
		}`),
	}
}

type channelReadArgs struct {
	Name string `json:"name"`
}

// channelReadEntry mirrors config.Channel but with explicit JSON tags so the
// tool response shape is stable and ordered for the agent. We don't return
// config.Channel directly because its YAML tags would surface as snake_case
// inconsistently across embedded types.
type channelReadEntry struct {
	Name                 string           `json:"name"`
	Type                 string           `json:"type"`
	Description          string           `json:"description"`
	Purpose              string           `json:"purpose,omitempty"`
	Model                string           `json:"model,omitempty"`
	MaxTurns             int              `json:"max_turns,omitempty"`
	Parent               string           `json:"parent,omitempty"`
	ToolGroups           []string         `json:"tool_groups,omitempty"`
	AllowedTools         []string         `json:"allowed_tools,omitempty"`
	DisallowedTools      []string         `json:"disallowed_tools,omitempty"`
	CreatableGroups      []string         `json:"creatable_groups,omitempty"`
	Links                []channel.Link   `json:"links,omitempty"`
	NotifyLifecycle      bool             `json:"notify_lifecycle,omitempty"`
	Ephemeral            bool             `json:"ephemeral,omitempty"`
	EphemeralIdleTimeout string           `json:"ephemeral_idle_timeout,omitempty"`
	ClaudeSessionTimeout string           `json:"claude_session_timeout,omitempty"`
	InitialMessage       string           `json:"initial_message,omitempty"`
	CreatedAt            string           `json:"created_at,omitempty"`
	Envs                 []string         `json:"envs,omitempty"`
	Telegram             *telegramSummary `json:"telegram,omitempty"`
}

// telegramSummary surfaces telegram metadata that doesn't leak the bot token —
// the agent has no business reading credentials back out of config.
type telegramSummary struct {
	HasToken bool `json:"has_token"`
}

func channelReadHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelReadArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Name == "" {
			return nil, fmt.Errorf("name is required")
		}

		channels, err := deps.ConfigWriter.ReadChannels(deps.UserID)
		if err != nil {
			return nil, fmt.Errorf("read channels: %w", err)
		}

		for _, ch := range channels {
			if ch.Name != a.Name {
				continue
			}
			entry := channelReadEntry{
				Name:                 ch.Name,
				Type:                 string(ch.Type),
				Description:          ch.Description,
				Purpose:              ch.Purpose,
				Model:                string(ch.Model),
				MaxTurns:             ch.MaxTurns,
				Parent:               ch.Parent,
				ToolGroups:           toolGroupNames(ch.ToolGroups),
				AllowedTools:         ch.AllowedTools,
				DisallowedTools:      ch.DisallowedTools,
				CreatableGroups:      toolGroupNames(ch.CreatableGroups),
				Links:                ch.Links,
				NotifyLifecycle:      ch.NotifyLifecycle,
				Ephemeral:            ch.Ephemeral,
				EphemeralIdleTimeout: ch.EphemeralIdleTimeout,
				ClaudeSessionTimeout: ch.ClaudeSessionTimeout,
				InitialMessage:       ch.InitialMessage,
				CreatedAt:            ch.CreatedAt,
				Envs:                 envNames(ch.Envs),
			}
			if ch.Telegram != nil {
				entry.Telegram = &telegramSummary{HasToken: ch.Telegram.Token != ""}
			}
			return json.Marshal(entry)
		}

		return nil, fmt.Errorf("channel %q not found", a.Name)
	}
}

func toolGroupNames(groups []toolgroup.ToolGroup) []string {
	if len(groups) == 0 {
		return nil
	}
	out := make([]string, len(groups))
	for i, g := range groups {
		out[i] = string(g)
	}
	return out
}

func envNames(envs []config.Env) []string {
	if len(envs) == 0 {
		return nil
	}
	out := make([]string, len(envs))
	for i, e := range envs {
		out[i] = string(e)
	}
	return out
}
