package channeltools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"tclaw/channel"
	"tclaw/mcp"
	"tclaw/role"
)

const (
	maxChannelNameLength        = 64
	maxChannelDescriptionLength = 512
)

var channelNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func channelCreateDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "channel_create",
		Description: "Create a new dynamic channel. Supported types: 'socket' (local only) and 'telegram' (requires a bot token). The agent restarts automatically to activate the new channel.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Short name for the channel (e.g. 'phone', 'tablet'). Used in routing and must be unique across all channels."
				},
				"description": {
					"type": "string",
					"description": "Describes the device or context (e.g. 'Mobile phone', 'Work tablet'). Helps the agent tailor responses."
				},
				"type": {
					"type": "string",
					"enum": ["socket", "telegram"],
					"description": "Channel transport type. 'socket' creates a unix domain socket (local environments only). 'telegram' creates a Telegram bot channel (requires telegram_config)."
				},
				"telegram_config": {
					"type": "object",
					"description": "Configuration for Telegram channels. Required when type is 'telegram'.",
					"properties": {
						"token": {
							"type": "string",
							"description": "Bot token from @BotFather. Stored encrypted in the secret store."
						},
						"allowed_users": {
							"type": "array",
							"items": {"type": "integer"},
							"description": "Telegram user IDs allowed to interact with this bot. When set, messages from other users are silently ignored. Get your user ID from @userinfobot on Telegram."
						}
					},
					"required": ["token", "allowed_users"]
				},
				"role": {
					"type": "string",
					"enum": ["superuser", "developer", "assistant"],
					"description": "Named preset of tool permissions. Mutually exclusive with allowed_tools — set one or the other. Roles resolve dynamically based on connections and remote MCPs on this channel."
				},
				"allowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tools this channel is allowed to use. Mutually exclusive with role. Supports glob patterns (e.g. 'mcp__tclaw__google_*') and builtin commands (e.g. 'builtin__reset_session', 'builtin__stop')."
				},
				"disallowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tools explicitly denied on this channel. Works alongside both role and allowed_tools for surgical removal."
				}
			},
			"required": ["name", "description", "type"]
		}`),
	}
}

type telegramCreateConfig struct {
	Token        string  `json:"token"`
	AllowedUsers []int64 `json:"allowed_users"`
}

type channelCreateArgs struct {
	Name            string                `json:"name"`
	Description     string                `json:"description"`
	Type            string                `json:"type"`
	TelegramConfig  *telegramCreateConfig `json:"telegram_config"`
	Role            string                `json:"role"`
	AllowedTools    []string              `json:"allowed_tools"`
	DisallowedTools []string              `json:"disallowed_tools"`
}

func channelCreateHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelCreateArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Name == "" || len(a.Name) > maxChannelNameLength {
			return nil, fmt.Errorf("name is required and must be under %d characters", maxChannelNameLength)
		}
		if !channelNamePattern.MatchString(a.Name) {
			return nil, fmt.Errorf("name must be alphanumeric with hyphens/underscores (no spaces or special characters)")
		}
		if a.Description == "" || len(a.Description) > maxChannelDescriptionLength {
			return nil, fmt.Errorf("description is required and must be under %d characters", maxChannelDescriptionLength)
		}

		var channelType channel.ChannelType
		switch a.Type {
		case "socket":
			if !deps.Env.IsLocal() {
				return nil, fmt.Errorf("socket channels are not allowed in %q environment (no authentication)", deps.Env)
			}
			channelType = channel.TypeSocket
		case "telegram":
			if a.TelegramConfig == nil || a.TelegramConfig.Token == "" {
				return nil, fmt.Errorf("telegram_config with a bot token is required for telegram channels (get a token from @BotFather)")
			}
			if len(a.TelegramConfig.AllowedUsers) == 0 {
				return nil, fmt.Errorf("telegram_config.allowed_users is required — at least one Telegram user ID must be specified (get your user ID from @userinfobot on Telegram)")
			}
			channelType = channel.TypeTelegram
		default:
			return nil, fmt.Errorf("unsupported channel type %q (must be 'socket' or 'telegram')", a.Type)
		}

		// Validate role / allowed_tools mutual exclusion.
		var channelRole role.Role
		if a.Role != "" {
			channelRole = role.Role(a.Role)
			if !channelRole.Valid() {
				return nil, fmt.Errorf("unknown role %q (known: %v)", a.Role, role.ValidRoles())
			}
			if len(a.AllowedTools) > 0 {
				return nil, fmt.Errorf("role and allowed_tools are mutually exclusive — set one or the other")
			}
		}

		// Check uniqueness against static channels.
		for _, info := range deps.StaticChannels {
			if info.Name == a.Name {
				return nil, fmt.Errorf("channel name %q is already used by a static channel (from config file)", a.Name)
			}
		}

		var allowedUsers []int64
		if a.TelegramConfig != nil {
			allowedUsers = a.TelegramConfig.AllowedUsers
		}

		cfg := channel.DynamicChannelConfig{
			Name:            a.Name,
			Type:            channelType,
			Description:     a.Description,
			CreatedAt:       time.Now(),
			Role:            channelRole,
			AllowedTools:    a.AllowedTools,
			DisallowedTools: a.DisallowedTools,
			AllowedUsers:    allowedUsers,
		}
		if err := deps.DynamicStore.Add(ctx, cfg); err != nil {
			return nil, fmt.Errorf("create channel: %w", err)
		}

		// Store the bot token in the encrypted secret store, not in the channel config.
		if a.TelegramConfig != nil {
			if err := deps.SecretStore.Set(ctx, channel.ChannelSecretKey(a.Name), a.TelegramConfig.Token); err != nil {
				// Roll back the channel config if we can't store the secret.
				if rollbackErr := deps.DynamicStore.Remove(ctx, a.Name); rollbackErr != nil {
					slog.Warn("failed to roll back channel config after secret store error", "channel", a.Name, "err", rollbackErr)
				}
				return nil, fmt.Errorf("store channel secret: %w", err)
			}
		}

		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		result := map[string]any{
			"name":        cfg.Name,
			"type":        string(cfg.Type),
			"description": cfg.Description,
			"message":     fmt.Sprintf("Channel %q created. The agent will restart automatically to activate it.", a.Name),
		}
		return json.Marshal(result)
	}
}
