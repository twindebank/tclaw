package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"tclaw/channel"
	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/toolgroup"
	"tclaw/user"

	"gopkg.in/yaml.v3"
)

// channelNamePattern restricts channel names to safe characters only.
// Prevents path traversal when names are used in filesystem paths or URL routes.
var channelNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// resolvedSecretCache stores secrets resolved during initial Load so that
// ReloadConfig can re-resolve them after env vars have been scrubbed.
var (
	resolvedSecretCacheMu sync.RWMutex
	resolvedSecretCache   = make(map[string]string)
)

// Config is the top-level configuration.
type Config struct {
	// BaseDir is the root for all per-user data (home dirs, stores).
	// Defaults to /tmp/tclaw if not set.
	BaseDir string `yaml:"base_dir"`

	// Env identifies the environment this process runs in (e.g. "local", "prod").
	// Used to filter channels via the Envs field. Defaults to EnvLocal.
	Env Env `yaml:"env"`

	// Server configures the HTTP server (health checks, OAuth callbacks, webhooks).
	Server ServerConfig `yaml:"server"`

	// Credentials provides pre-configured OAuth client credentials keyed by
	// tool package name (e.g. "google", "monzo"). These are seeded into the
	// credential system at startup so the agent doesn't need to collect them.
	Credentials CredentialsConfig `yaml:"credentials"`

	Users []User `yaml:"users"`
}

// ServerConfig holds settings for the HTTP server that handles health checks,
// OAuth callbacks, and Telegram webhooks.
type ServerConfig struct {
	// Addr is the listen address for the HTTP server.
	// Defaults to "127.0.0.1:9876".
	Addr string `yaml:"addr"`

	// PublicURL is the externally-reachable base URL (e.g. "https://your-app.fly.dev").
	// When set, Telegram channels use webhooks instead of long polling.
	PublicURL string `yaml:"public_url"`
}

// CredentialsConfig maps tool package names to lists of credential entries.
// Each key (e.g. "google", "monzo") matches the tool package's Name().
// Entries are seeded into credential sets at startup.
type CredentialsConfig map[string][]CredentialEntry

// CredentialEntry is a single credential set definition from the config file.
type CredentialEntry struct {
	Label   string            `yaml:"label"`
	Channel string            `yaml:"channel,omitempty"`
	Secrets map[string]string `yaml:"secrets"`
}

// User defines per-user agent configuration.
type User struct {
	ID             user.ID                  `yaml:"id"`
	APIKey         string                   `yaml:"api_key"`
	Model          claudecli.Model          `yaml:"model"`
	PermissionMode claudecli.PermissionMode `yaml:"permission_mode"`
	MaxTurns       int                      `yaml:"max_turns"`
	Debug          bool                     `yaml:"debug"`

	AllowedTools    []claudecli.Tool `yaml:"allowed_tools"`
	DisallowedTools []claudecli.Tool `yaml:"disallowed_tools"`
	SystemPrompt    string           `yaml:"system_prompt"`

	// Telegram holds the user's Telegram identity. All Telegram channels
	// for this user inherit these settings.
	Telegram *UserTelegramConfig `yaml:"telegram,omitempty"`

	Channels []Channel `yaml:"channels"`
}

// UserTelegramConfig holds user-level Telegram settings.
type UserTelegramConfig struct {
	// UserID is the Telegram user ID. All Telegram channels for this user
	// restrict access to this ID.
	UserID string `yaml:"user_id"`
}

// Channel defines a channel attached to a user.
// Type, Name, and Description are required; other fields depend on the transport.
type Channel struct {
	Type        ChannelType `yaml:"type"`
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`

	// Purpose is optional behavioral guidance for the agent operating on this
	// channel. Unlike Description (which describes the device/context), Purpose
	// tells the agent what kind of work this channel is for and how to behave.
	Purpose string `yaml:"purpose,omitempty"`

	// Telegram holds Telegram-specific channel config.
	// Non-nil when Type is "telegram".
	Telegram *TelegramChannelConfig `yaml:"telegram,omitempty"`

	// Envs restricts this channel to specific environments.
	// Empty means the channel is active in all environments.
	Envs []Env `yaml:"envs,omitempty"`

	// ToolGroups is a list of named tool groups, combined additively.
	// Mutually exclusive with AllowedTools.
	ToolGroups []toolgroup.ToolGroup `yaml:"tool_groups,omitempty"`

	// AllowedTools overrides the user-level allowed_tools for this channel.
	// Mutually exclusive with ToolGroups. When set, this replaces
	// (not merges with) the user-level list.
	AllowedTools []string `yaml:"allowed_tools,omitempty"`

	// DisallowedTools overrides user-level disallowed_tools for this channel.
	// Works alongside ToolGroups and AllowedTools for surgical removal.
	DisallowedTools []string `yaml:"disallowed_tools,omitempty"`

	// CreatableGroups is the set of tool groups this channel can delegate when
	// creating new channels via channel_create. If empty, channel_create is
	// blocked on this channel.
	CreatableGroups []toolgroup.ToolGroup `yaml:"creatable_groups,omitempty"`

	// NotifyLifecycle sends a message to this channel on instance startup and shutdown.
	NotifyLifecycle bool `yaml:"notify_lifecycle,omitempty"`

	// Links declares which channels this channel can send messages to via
	// the channel_send MCP tool. Only declared links are valid — the agent
	// cannot send to arbitrary channels.
	Links []ChannelLink `yaml:"links,omitempty"`

	// Ephemeral marks this channel for automatic cleanup after idle timeout.
	// When true, the channel is removed from config after EphemeralIdleTimeout
	// of inactivity.
	Ephemeral bool `yaml:"ephemeral,omitempty"`

	// EphemeralIdleTimeout is how long an ephemeral channel can sit idle before
	// auto-cleanup. Parsed as a Go duration string (e.g. "24h", "30m").
	// Defaults to 24 hours. Only meaningful when Ephemeral is true.
	EphemeralIdleTimeout string `yaml:"ephemeral_idle_timeout,omitempty"`

	// InitialMessage is delivered to the channel as its first inbound message
	// once the channel comes online after creation. Cleared after delivery so
	// it fires exactly once.
	InitialMessage string `yaml:"initial_message,omitempty"`

	// Parent is the name of the channel that created this one. Lifecycle events
	// (ephemeral teardown, build failures) are reported to the parent via the
	// message queue so the agent on that channel can react.
	Parent string `yaml:"parent,omitempty"`

	// CreatedAt is the RFC3339 timestamp of when this channel was created by
	// a tool. Empty for hand-written channels.
	CreatedAt string `yaml:"created_at,omitempty"`
}

// ChannelLink is a config alias for channel.Link with YAML tags.
type ChannelLink = channel.Link

// TelegramChannelConfig holds Telegram-specific channel configuration.
type TelegramChannelConfig struct {
	// Token is the Telegram bot token from @BotFather.
	// Supports secret references: ${secret:NAME}.
	Token string `yaml:"token"`
}

// Env identifies the runtime environment.
type Env string

const (
	EnvLocal Env = "local"
	EnvProd  Env = "prod"
)

// IsLocal returns true if this is the local development environment.
func (e Env) IsLocal() bool { return e == EnvLocal }

// HasEnv checks whether the config file at path contains a section for the
// given environment. Useful for detecting whether a prod deployment is
// configured without fully loading the config.
func HasEnv(path string, env Env) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var envMap map[string]yaml.Node
	if err := yaml.Unmarshal(data, &envMap); err != nil {
		return false
	}
	_, ok := envMap[string(env)]
	return ok
}

// ChannelType is an alias for channel.ChannelType to avoid repeating
// the type definition. Config YAML values unmarshal into channel's type.
type ChannelType = channel.ChannelType

const (
	ChannelTypeSocket   = channel.TypeSocket
	ChannelTypeStdio    = channel.TypeStdio
	ChannelTypeTelegram = channel.TypeTelegram
)

// Load reads a multi-environment config file and returns the Config for the
// given environment. The file is keyed by environment name at the top level
// (e.g. "local:", "prod:"). Any environment variables consumed during secret
// resolution are immediately unset so they cannot leak to subprocesses.
func Load(path string, env Env) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Parse as a map of env name → raw YAML, then decode the requested env.
	var envMap map[string]yaml.Node
	if err := yaml.Unmarshal(data, &envMap); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	node, ok := envMap[string(env)]
	if !ok {
		var available []string
		for k := range envMap {
			available = append(available, k)
		}
		return nil, fmt.Errorf("environment %q not found in config (available: %v)", env, available)
	}

	var cfg Config
	if err := node.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config for env %q: %w", env, err)
	}

	// Set the env from the key — no need to duplicate it in the YAML body.
	cfg.Env = env

	if cfg.BaseDir == "" {
		cfg.BaseDir = "/tmp/tclaw"
	}
	if cfg.Server.Addr == "" {
		cfg.Server.Addr = "127.0.0.1:9876"
	}

	resolvedEnvVars, err := resolveSecrets(&cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve secrets: %w", err)
	}

	// Scrub secret-bearing env vars so subprocesses can't read them.
	for _, name := range resolvedEnvVars {
		os.Unsetenv(name)
		slog.Debug("scrubbed secret env var", "name", name)
	}

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func validate(cfg *Config) error {
	if len(cfg.Users) == 0 {
		return fmt.Errorf("no users defined")
	}

	seen := make(map[user.ID]bool)
	for i, u := range cfg.Users {
		if u.ID == "" {
			return fmt.Errorf("user %d: missing id", i)
		}
		if seen[u.ID] {
			return fmt.Errorf("user %d: duplicate id %q", i, u.ID)
		}
		seen[u.ID] = true

		if u.Model != "" && !claudecli.ValidModel(u.Model) {
			return fmt.Errorf("user %q: unknown model %q (known: %v)", u.ID, u.Model, claudecli.ValidModels())
		}

		if u.PermissionMode != "" && !claudecli.ValidPermissionMode(u.PermissionMode) {
			return fmt.Errorf("user %q: unknown permission_mode %q (known: %v)", u.ID, u.PermissionMode, claudecli.ValidPermissionModes())
		}

		for j, t := range u.AllowedTools {
			if !claudecli.ValidTool(t) {
				return fmt.Errorf("user %q allowed_tools[%d]: unknown tool %q", u.ID, j, t)
			}
		}
		for j, t := range u.DisallowedTools {
			if !claudecli.ValidTool(t) {
				return fmt.Errorf("user %q disallowed_tools[%d]: unknown tool %q", u.ID, j, t)
			}
		}

		if len(u.Channels) == 0 {
			return fmt.Errorf("user %q: no channels defined", u.ID)
		}

		chNames := make(map[string]bool)
		for j, ch := range u.Channels {
			if ch.Name == "" {
				return fmt.Errorf("user %q channel %d: missing name", u.ID, j)
			}
			if !channelNamePattern.MatchString(ch.Name) {
				return fmt.Errorf("user %q channel %d: name %q contains invalid characters (must match %s)", u.ID, j, ch.Name, channelNamePattern.String())
			}
			if chNames[ch.Name] {
				return fmt.Errorf("user %q channel %d: duplicate name %q", u.ID, j, ch.Name)
			}
			chNames[ch.Name] = true

			if ch.Description == "" {
				return fmt.Errorf("user %q channel %q: missing description", u.ID, ch.Name)
			}

			for k, t := range ch.AllowedTools {
				if !claudecli.ValidTool(claudecli.Tool(t)) {
					return fmt.Errorf("user %q channel %q allowed_tools[%d]: unknown tool %q", u.ID, ch.Name, k, t)
				}
			}
			for k, t := range ch.DisallowedTools {
				if !claudecli.ValidTool(claudecli.Tool(t)) {
					return fmt.Errorf("user %q channel %q disallowed_tools[%d]: unknown tool %q", u.ID, ch.Name, k, t)
				}
			}

			if ch.EphemeralIdleTimeout != "" {
				if _, err := time.ParseDuration(ch.EphemeralIdleTimeout); err != nil {
					return fmt.Errorf("user %q channel %q: invalid ephemeral_idle_timeout %q: %w", u.ID, ch.Name, ch.EphemeralIdleTimeout, err)
				}
			}

			if ch.CreatedAt != "" {
				if _, err := time.Parse(time.RFC3339, ch.CreatedAt); err != nil {
					return fmt.Errorf("user %q channel %q: invalid created_at %q: %w", u.ID, ch.Name, ch.CreatedAt, err)
				}
			}

			switch ch.Type {
			case ChannelTypeSocket, ChannelTypeStdio:
				// valid — no token or allowed_users needed
			case ChannelTypeTelegram:
				// Token may be empty for channels that need provisioning —
				// the reconciler will provision and populate it.
				if u.Telegram == nil || u.Telegram.UserID == "" {
					return fmt.Errorf("user %q channel %q: telegram channels require user-level telegram.user_id", u.ID, ch.Name)
				}
			case "":
				return fmt.Errorf("user %q channel %q: missing type", u.ID, ch.Name)
			default:
				return fmt.Errorf("user %q channel %q: unknown type %q (known: socket, stdio, telegram)", u.ID, ch.Name, ch.Type)
			}

		}

		// Validate channel links in a second pass so forward references work
		// (a link can target a channel defined later in the list).
		for _, ch := range u.Channels {
			linkTargets := make(map[string]bool)
			for k, link := range ch.Links {
				if link.Target == "" {
					return fmt.Errorf("user %q channel %q links[%d]: missing target", u.ID, ch.Name, k)
				}
				if link.Description == "" {
					return fmt.Errorf("user %q channel %q links[%d]: missing description", u.ID, ch.Name, k)
				}
				if link.Target == ch.Name {
					return fmt.Errorf("user %q channel %q links[%d]: self-links are not allowed", u.ID, ch.Name, k)
				}
				if linkTargets[link.Target] {
					return fmt.Errorf("user %q channel %q links[%d]: duplicate target %q", u.ID, ch.Name, k, link.Target)
				}
				linkTargets[link.Target] = true
				if !chNames[link.Target] {
					return fmt.Errorf("user %q channel %q links[%d]: target %q does not match any channel name", u.ID, ch.Name, k, link.Target)
				}
			}
		}
	}

	return nil
}

const (
	secretRefPrefix = "${secret:"
	refSuffix       = "}"
)

// resolveSecrets expands secret references in config fields and returns the
// names of any environment variables that were read during resolution.
//
// Supported syntax:
//
//	${secret:NAME}  — tries OS keychain for NAME, falls back to env var NAME
//	literal         — used as-is
func resolveSecrets(cfg *Config) ([]string, error) {
	var envVars []string

	for i := range cfg.Users {
		val, envVar, err := resolveRef(cfg.Users[i].APIKey)
		if err != nil {
			return nil, fmt.Errorf("user %q api_key: %w", cfg.Users[i].ID, err)
		}
		cfg.Users[i].APIKey = val
		if envVar != "" {
			envVars = append(envVars, envVar)
		}

		// Resolve Telegram bot tokens.
		for j := range cfg.Users[i].Channels {
			tc := cfg.Users[i].Channels[j].Telegram
			if tc == nil || tc.Token == "" {
				continue
			}
			val, envVar, err := resolveRef(tc.Token)
			if err != nil {
				return nil, fmt.Errorf("user %q channel %q telegram.token: %w", cfg.Users[i].ID, cfg.Users[i].Channels[j].Name, err)
			}
			tc.Token = val
			if envVar != "" {
				envVars = append(envVars, envVar)
			}
		}
	}

	// Resolve credential secret references.
	for pkg, entries := range cfg.Credentials {
		for i, entry := range entries {
			for key, val := range entry.Secrets {
				resolved, envVar, err := resolveRef(val)
				if err != nil {
					return nil, fmt.Errorf("credentials.%s[%d].secrets.%s: %w", pkg, i, key, err)
				}
				entry.Secrets[key] = resolved
				if envVar != "" {
					envVars = append(envVars, envVar)
				}
			}
			cfg.Credentials[pkg][i] = entry
		}
	}

	return envVars, nil
}

// resolveRef resolves a single config value. Returns the resolved value and,
// if an environment variable was read, its name (so callers can scrub it).
func resolveRef(s string) (string, string, error) {
	if !strings.HasPrefix(s, secretRefPrefix) || !strings.HasSuffix(s, refSuffix) {
		// Not a secret reference — use as literal.
		return s, "", nil
	}

	name := s[len(secretRefPrefix) : len(s)-len(refSuffix)]
	return resolveSecret(name)
}

// resolveSecret tries the in-memory cache first (populated on initial load),
// then the OS keychain, then falls back to env var. Resolved values are cached
// so config reloads succeed after env vars have been scrubbed.
func resolveSecret(name string) (string, string, error) {
	resolvedSecretCacheMu.RLock()
	cached, ok := resolvedSecretCache[name]
	resolvedSecretCacheMu.RUnlock()
	if ok {
		return cached, "", nil
	}

	if secret.KeychainAvailable() {
		ks := secret.NewKeychainStore("_config")
		val, err := ks.Get(context.Background(), name)
		if err != nil {
			return "", "", fmt.Errorf("keychain lookup %q: %w", name, err)
		}
		if val != "" {
			slog.Debug("resolved secret from keychain", "name", name)
			cacheResolvedSecret(name, val)
			return val, "", nil
		}
	}

	// Fall back to env var with the same name.
	val := os.Getenv(name)
	if val != "" {
		slog.Debug("resolved secret from env var", "name", name)
		cacheResolvedSecret(name, val)
		return val, name, nil
	}

	return "", "", fmt.Errorf("secret %q not found in keychain or env var", name)
}

func cacheResolvedSecret(name, value string) {
	resolvedSecretCacheMu.Lock()
	resolvedSecretCache[name] = value
	resolvedSecretCacheMu.Unlock()
}

// ToUserConfig converts a config User to a user.Config (without system-derived fields).
func (u *User) ToUserConfig() user.Config {
	return user.Config{
		ID:              u.ID,
		APIKey:          u.APIKey,
		Model:           u.Model,
		PermissionMode:  u.PermissionMode,
		AllowedTools:    u.AllowedTools,
		DisallowedTools: u.DisallowedTools,
		MaxTurns:        u.MaxTurns,
		Debug:           u.Debug,
		SystemPrompt:    u.SystemPrompt,
	}
}
