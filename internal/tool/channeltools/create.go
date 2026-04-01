package channeltools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/config"
	"tclaw/internal/mcp"
	"tclaw/internal/reconciler"
	"tclaw/internal/toolgroup"
)

const (
	maxChannelNameLength        = 64
	maxChannelDescriptionLength = 512
)

var channelNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

const ToolChannelCreate = "channel_create"

func channelCreateDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolChannelCreate,
		Description: "Create a new channel by adding it to config. " +
			"If the platform supports auto-provisioning (e.g. Telegram with Client API), " +
			"the channel is provisioned synchronously and the result is returned immediately. " +
			"If provisioning fails, the error is returned so you can fix the issue and retry. " +
			"If auto-provisioning is not available, the channel is created with status 'needs_setup' " +
			"and the agent guides the user through manual setup.\n\n" +
			"Set ephemeral: true for channels that should auto-delete after idle timeout. " +
			"Use initial_message to deliver a kick-off task to the new channel on first boot.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Short name for the channel (e.g. 'phone', 'email-check-20260323'). Must be unique."
				},
				"description": {
					"type": "string",
					"description": "Describes the channel's device or context. For Telegram, used as the bot display name (max 56 chars)."
				},
				"purpose": {
					"type": "string",
					"description": "Optional behavioral guidance for the agent on this channel. Defines what kind of work the channel is for and how the agent should behave (e.g. 'Day-to-day personal tasks: email, calendar, travel. No dev work.'). Persisted in the system prompt."
				},
				"type": {
					"type": "string",
					"enum": ["socket", "telegram"],
					"description": "Channel transport type."
				},
				"allowed_users": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Platform user IDs for access control. Required for some channel types."
				},
				"ephemeral": {
					"type": "boolean",
					"description": "If true, the channel auto-deletes after idle timeout."
				},
				"ephemeral_idle_timeout_hours": {
					"type": "integer",
					"description": "Hours before an idle ephemeral channel is cleaned up. Defaults to 24."
				},
				"tool_groups": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tool groups to enable. Mutually exclusive with allowed_tools. Use tool_group_list to see available groups."
				},
				"allowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Explicit tool list. Mutually exclusive with tool_groups."
				},
				"disallowed_tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tools explicitly denied on this channel."
				},
				"creatable_groups": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tool groups this channel can delegate when creating new channels. Prevents privilege escalation."
				},
				"links": {
					"type": "array",
					"description": "Cross-channel messaging links.",
					"items": {
						"type": "object",
						"properties": {
							"target": {"type": "string", "description": "Name of the target channel."},
							"description": {"type": "string", "description": "When this link should be used."}
						},
						"required": ["target", "description"]
					}
				},
				"initial_message": {
					"type": "string",
					"description": "Message delivered to the new channel as its first inbound message. Fires exactly once."
				},
				"parent": {
					"type": "string",
					"description": "Parent channel name. The parent receives lifecycle notifications (e.g. ephemeral teardown, build failures). Should be set to the current channel when creating child channels."
				}
			},
			"required": ["name", "description", "type"]
		}`),
	}
}

type channelCreateArgs struct {
	Name                      string         `json:"name"`
	Description               string         `json:"description"`
	Purpose                   string         `json:"purpose"`
	Type                      string         `json:"type"`
	Ephemeral                 bool           `json:"ephemeral"`
	EphemeralIdleTimeoutHours int            `json:"ephemeral_idle_timeout_hours"`
	ToolGroups                []string       `json:"tool_groups"`
	AllowedTools              []string       `json:"allowed_tools"`
	DisallowedTools           []string       `json:"disallowed_tools"`
	CreatableGroups           []string       `json:"creatable_groups"`
	Links                     []channel.Link `json:"links"`
	InitialMessage            string         `json:"initial_message"`
	Parent                    string         `json:"parent"`
}

func channelCreateHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a channelCreateArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		// Validate inputs.
		if a.Name == "" || len(a.Name) > maxChannelNameLength {
			return nil, fmt.Errorf("name is required and must be under %d characters", maxChannelNameLength)
		}
		if !channelNamePattern.MatchString(a.Name) {
			return nil, fmt.Errorf("name must be alphanumeric with hyphens/underscores")
		}
		if a.Description == "" || len(a.Description) > maxChannelDescriptionLength {
			return nil, fmt.Errorf("description is required and must be under %d characters", maxChannelDescriptionLength)
		}

		channelType := channel.ChannelType(a.Type)
		switch channelType {
		case channel.TypeSocket:
			if !deps.Env.IsLocal() {
				return nil, fmt.Errorf("socket channels are not allowed in %q environment (no authentication)", deps.Env)
			}
		case channel.TypeTelegram:
			provisioner := deps.Provisioners.Get(channelType)
			if provisioner != nil {
				// Platform-specific validation via provisioner.
				if err := provisioner.ValidateCreate(a.Description); err != nil {
					return nil, err
				}
			} else {
				// No provisioner — Telegram Client API credentials are not configured.
				// Check if a bot token already exists (from manual setup).
				token, _ := deps.SecretStore.Get(ctx, channel.ChannelSecretKey(a.Name))
				if token == "" {
					return nil, fmt.Errorf("cannot create Telegram channel %q: auto-provisioning is unavailable (Telegram Client API credentials not configured). "+
						"To fix, either: (1) run telegram_client_info to check credential status and set up Telegram Client API credentials for auto-provisioning, "+
						"or (2) ask the user to create a bot manually via @BotFather, then store the token with secret_form_request. "+
						"After setting up credentials via either path, call channel_create again to retry — the channel is NOT written to config until creation succeeds", a.Name)
				}
			}
		default:
			return nil, fmt.Errorf("unsupported channel type %q", a.Type)
		}

		// Validate tool_groups / allowed_tools mutual exclusion.
		if len(a.ToolGroups) > 0 && len(a.AllowedTools) > 0 {
			return nil, fmt.Errorf("tool_groups and allowed_tools are mutually exclusive — set exactly one")
		}

		var toolGroups []toolgroup.ToolGroup
		for _, g := range a.ToolGroups {
			tg := toolgroup.ToolGroup(g)
			if !toolgroup.ValidGroup(tg) {
				return nil, fmt.Errorf("unknown tool group %q", g)
			}
			toolGroups = append(toolGroups, tg)
		}

		// Enforce creatable_groups — creating channel can only delegate authorized groups.
		if deps.ActiveChannel != nil && len(toolGroups) > 0 {
			activeChannelName := deps.ActiveChannel()
			if activeChannelName != "" {
				if activeEntry := deps.Registry.ByName(activeChannelName); activeEntry != nil {
					creatableSet := make(map[string]bool, len(activeEntry.CreatableGroups))
					for _, g := range activeEntry.CreatableGroups {
						creatableSet[g] = true
					}
					if len(creatableSet) == 0 {
						return nil, fmt.Errorf("this channel is not authorized to create other channels (creatable_groups is empty)")
					}
					for _, g := range toolGroups {
						if !creatableSet[string(g)] {
							return nil, fmt.Errorf("this channel is not authorized to delegate tool group %q (not in creatable_groups)", g)
						}
					}
				}
			}
		}

		for _, g := range a.CreatableGroups {
			if !toolgroup.ValidGroup(toolgroup.ToolGroup(g)) {
				return nil, fmt.Errorf("unknown creatable group %q", g)
			}
		}

		// Check name uniqueness.
		if deps.Registry.NameExists(a.Name) {
			return nil, fmt.Errorf("channel %q already exists", a.Name)
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

		if a.Parent != "" && !deps.Registry.NameExists(a.Parent) {
			return nil, fmt.Errorf("parent channel %q not found", a.Parent)
		}

		// Build the idle timeout string for config.
		var idleTimeoutStr string
		if a.Ephemeral {
			timeout := 24 * time.Hour
			if a.EphemeralIdleTimeoutHours > 0 {
				timeout = time.Duration(a.EphemeralIdleTimeoutHours) * time.Hour
			}
			idleTimeoutStr = timeout.String()
		}

		// Build the config channel entry. Provisioning happens synchronously via
		// ReconcileOne below so the agent gets immediate feedback.
		ch := config.Channel{
			Type:                 channelType,
			Name:                 a.Name,
			Description:          a.Description,
			Purpose:              a.Purpose,
			ToolGroups:           toolGroups,
			AllowedTools:         a.AllowedTools,
			DisallowedTools:      a.DisallowedTools,
			CreatableGroups:      creatableGroupsToToolGroups(a.CreatableGroups),
			Links:                a.Links,
			Ephemeral:            a.Ephemeral,
			EphemeralIdleTimeout: idleTimeoutStr,
			InitialMessage:       a.InitialMessage,
			Parent:               a.Parent,
			CreatedAt:            time.Now().Format(time.RFC3339),
		}

		if err := deps.ConfigWriter.AddChannel(deps.UserID, ch); err != nil {
			return nil, fmt.Errorf("add channel to config: %w", err)
		}

		// Reconcile synchronously so the agent gets immediate feedback on
		// whether provisioning succeeded or the channel needs manual setup.
		rc, reconcileErr := reconciler.ReconcileOne(ctx, ch, deps.ReconcileParams)
		if reconcileErr != nil {
			// Config was written but provisioning failed — leave the channel in
			// config so the user can fix the issue and retry.
			return nil, fmt.Errorf("channel added to config but provisioning failed: %w", reconcileErr)
		}

		slog.Info("channel created", "channel", a.Name, "type", a.Type, "status", rc.Status)

		// Signal the router to restart and build the transport.
		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		result := map[string]any{
			"name":        a.Name,
			"type":        a.Type,
			"description": a.Description,
		}

		switch rc.Status {
		case reconciler.ChannelReady:
			result["status"] = "ready"
			result["message"] = fmt.Sprintf("Channel %q created and provisioned. The agent will restart to activate it.", a.Name)

			// Include platform-specific info if available.
			if prov := deps.Provisioners.Get(channelType); prov != nil && rc.RuntimeState != nil {
				if info := prov.PlatformResponseInfo(rc.RuntimeState.TeardownState); info != nil {
					for k, v := range info {
						result[k] = v
					}
				}
			}
		case reconciler.ChannelNeedsSetup:
			result["status"] = "needs_setup"
			result["message"] = fmt.Sprintf("Channel %q added to config but needs manual setup — provide a bot token or configure Telegram Client API credentials for auto-provisioning.", a.Name)
		}

		return json.Marshal(result)
	}
}

func creatableGroupsToToolGroups(groups []string) []toolgroup.ToolGroup {
	result := make([]toolgroup.ToolGroup, len(groups))
	for i, g := range groups {
		result[i] = toolgroup.ToolGroup(g)
	}
	return result
}
