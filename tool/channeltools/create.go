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
		Name: "channel_create",
		Description: "Create a new dynamic channel. Supported types: 'socket' (local only) and 'telegram'. " +
			"For Telegram: if telegram_client_setup has been completed, the bot is created automatically " +
			"(no manual @BotFather needed). Otherwise, provide a token in telegram_config. " +
			"Set ephemeral: true for channels that should auto-delete after idle timeout (default 24h). " +
			"The agent restarts automatically to activate the new channel.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Short name for the channel (e.g. 'phone', 'email-check-20260323'). Used in routing and must be unique."
				},
				"description": {
					"type": "string",
					"description": "Describes the device or context. Helps the agent tailor responses."
				},
				"type": {
					"type": "string",
					"enum": ["socket", "telegram"],
					"description": "Channel transport type."
				},
				"telegram_config": {
					"type": "object",
					"description": "Manual Telegram config. Only needed if Telegram Client API is not set up. Omit to auto-create a bot.",
					"properties": {
						"token": {
							"type": "string",
							"description": "Bot token from @BotFather."
						},
						"allowed_users": {
							"type": "array",
							"items": {"type": "integer"},
							"description": "Telegram user IDs allowed to interact with this bot."
						}
					},
					"required": ["token", "allowed_users"]
				},
				"ephemeral": {
					"type": "boolean",
					"description": "If true, the channel auto-deletes after idle timeout. Platform resources (e.g. Telegram bot) are cleaned up automatically. Use channel_done to tear down manually."
				},
				"ephemeral_idle_timeout_hours": {
					"type": "integer",
					"description": "How many hours an ephemeral channel can sit idle before auto-cleanup. Defaults to 24. Only meaningful when ephemeral is true."
				},
				"role": {
					"type": "string",
					"enum": ["superuser", "developer", "assistant"],
					"description": "Named preset of tool permissions. Mutually exclusive with allowed_tools."
				},
				"allowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tools this channel is allowed to use. Mutually exclusive with role."
				},
				"disallowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tools explicitly denied on this channel."
				},
				"links": {
					"type": "array",
					"description": "Cross-channel messaging links. For ephemeral channels, make these task-specific — describe exactly when to use each link based on what the job does.",
					"items": {
						"type": "object",
						"properties": {
							"target": {"type": "string", "description": "Name of the target channel."},
							"description": {"type": "string", "description": "When this link should be used. Be specific to the task, not generic."}
						},
						"required": ["target", "description"]
					}
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
	Name                      string                `json:"name"`
	Description               string                `json:"description"`
	Type                      string                `json:"type"`
	TelegramConfig            *telegramCreateConfig `json:"telegram_config"`
	Ephemeral                 bool                  `json:"ephemeral"`
	EphemeralIdleTimeoutHours int                   `json:"ephemeral_idle_timeout_hours"`
	Role                      string                `json:"role"`
	AllowedTools              []string              `json:"allowed_tools"`
	DisallowedTools           []string              `json:"disallowed_tools"`
	Links                     []channel.Link        `json:"links"`
}

const defaultEphemeralIdleTimeout = 24 * time.Hour

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
		var teardownState channel.TeardownState
		switch a.Type {
		case "socket":
			if !deps.Env.IsLocal() {
				return nil, fmt.Errorf("socket channels are not allowed in %q environment (no authentication)", deps.Env)
			}
			channelType = channel.TypeSocket
		case "telegram":
			// If no token provided, try auto-provisioning via the Telegram provisioner.
			if a.TelegramConfig == nil || a.TelegramConfig.Token == "" {
				provisioner, hasProvisioner := deps.Provisioners[channel.TypeTelegram]
				if !hasProvisioner {
					return nil, fmt.Errorf("Telegram Client API not configured — run telegram_client_setup first to enable automatic bot creation. Alternatively, create a bot manually via @BotFather and pass the token in telegram_config.")
				}

				result, provErr := provisioner.Provision(ctx, a.Name, a.Description)
				if provErr != nil {
					return nil, fmt.Errorf("auto-create Telegram bot: %w", provErr)
				}

				// Fill in the config from the provisioner result.
				a.TelegramConfig = &telegramCreateConfig{
					Token:        result.Token,
					AllowedUsers: result.AllowedUsers,
				}
				// Store teardown state for later cleanup.
				teardownState = result.TeardownState
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

		// Check uniqueness against all existing channels (static + dynamic).
		exists, existsErr := deps.Registry.NameExists(ctx, a.Name)
		if existsErr != nil {
			return nil, fmt.Errorf("check channel name: %w", existsErr)
		}
		if exists {
			if deps.Registry.IsStatic(a.Name) {
				return nil, fmt.Errorf("channel name %q is already used by a static channel (from config file)", a.Name)
			}
			return nil, fmt.Errorf("dynamic channel %q already exists", a.Name)
		}

		// Validate links.
		linkTargets := make(map[string]bool, len(a.Links))
		for i, link := range a.Links {
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

		var allowedUsers []int64
		if a.TelegramConfig != nil {
			allowedUsers = a.TelegramConfig.AllowedUsers
		}

		idleTimeout := defaultEphemeralIdleTimeout
		if a.EphemeralIdleTimeoutHours > 0 {
			idleTimeout = time.Duration(a.EphemeralIdleTimeoutHours) * time.Hour
		}

		cfg := channel.DynamicChannelConfig{
			Name:                 a.Name,
			Type:                 channelType,
			Description:          a.Description,
			CreatedAt:            time.Now(),
			Role:                 channelRole,
			AllowedTools:         a.AllowedTools,
			DisallowedTools:      a.DisallowedTools,
			AllowedUsers:         allowedUsers,
			Links:                a.Links,
			Ephemeral:            a.Ephemeral,
			EphemeralIdleTimeout: idleTimeout,
			TeardownState:        teardownState,
		}
		if err := deps.Registry.DynamicStore().Add(ctx, cfg); err != nil {
			return nil, fmt.Errorf("create channel: %w", err)
		}

		// Store the bot token in the encrypted secret store, not in the channel config.
		if a.TelegramConfig != nil {
			if err := deps.SecretStore.Set(ctx, channel.ChannelSecretKey(a.Name), a.TelegramConfig.Token); err != nil {
				// Roll back the channel config if we can't store the secret.
				if rollbackErr := deps.Registry.DynamicStore().Remove(ctx, a.Name); rollbackErr != nil {
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
