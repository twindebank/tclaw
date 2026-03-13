package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"tclaw/channel"
	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/role"
	"tclaw/user"

	"gopkg.in/yaml.v3"
)

// channelNamePattern restricts channel names to safe characters only.
// Prevents path traversal when names are used in filesystem paths or URL routes.
var channelNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

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

	// Providers configures external service providers (Gmail, etc.).
	Providers ProvidersConfig `yaml:"providers"`

	Users []User `yaml:"users"`
}

// ServerConfig holds settings for the HTTP server that handles health checks,
// OAuth callbacks, and Telegram webhooks.
type ServerConfig struct {
	// Addr is the listen address for the HTTP server.
	// Defaults to "127.0.0.1:9876".
	Addr string `yaml:"addr"`

	// PublicURL is the externally-reachable base URL (e.g. "https://tclaw.fly.dev").
	// When set, Telegram channels use webhooks instead of long polling.
	PublicURL string `yaml:"public_url"`
}

// ProvidersConfig holds per-provider configuration.
type ProvidersConfig struct {
	Google *GoogleProviderCredentials `yaml:"google"`
	Monzo  *MonzoProviderCredentials  `yaml:"monzo"`
}

// GoogleProviderCredentials holds OAuth client credentials for Google Workspace.
type GoogleProviderCredentials struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

// MonzoProviderCredentials holds OAuth client credentials for Monzo.
type MonzoProviderCredentials struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

// User defines per-user agent configuration.
type User struct {
	ID             user.ID                  `yaml:"id"`
	APIKey         string                   `yaml:"api_key"`
	Model          claudecli.Model          `yaml:"model"`
	PermissionMode claudecli.PermissionMode `yaml:"permission_mode"`
	MaxTurns       int                      `yaml:"max_turns"`
	Debug          bool                     `yaml:"debug"`

	// Role is a named preset of tool permissions. Mutually exclusive with
	// AllowedTools — set one or the other. When set on the user, it applies
	// as the default for all channels that don't specify their own role or
	// allowed_tools.
	Role role.Role `yaml:"role,omitempty"`

	AllowedTools    []claudecli.Tool `yaml:"allowed_tools"`
	DisallowedTools []claudecli.Tool `yaml:"disallowed_tools"`
	SystemPrompt    string           `yaml:"system_prompt"`

	Channels []Channel `yaml:"channels"`
}

// Channel defines a channel attached to a user.
// Type, Name, and Description are required; other fields depend on the transport.
type Channel struct {
	Type        ChannelType `yaml:"type"`
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`

	// TelegramConfig holds config specific to Telegram channels.
	// Required when Type is "telegram".
	TelegramConfig *TelegramChannelConfig `yaml:"telegram,omitempty"`

	// Envs restricts this channel to specific environments.
	// Empty means the channel is active in all environments.
	Envs []Env `yaml:"envs,omitempty"`

	// Role is a named preset of tool permissions for this channel.
	// Mutually exclusive with AllowedTools — set one or the other.
	// When set, this replaces any user-level role or allowed_tools.
	Role role.Role `yaml:"role,omitempty"`

	// AllowedTools overrides the user-level allowed_tools for this channel.
	// Mutually exclusive with Role. When set, this replaces (not merges
	// with) the user-level list.
	AllowedTools []string `yaml:"allowed_tools,omitempty"`

	// DisallowedTools overrides user-level disallowed_tools for this channel.
	// Works alongside both Role and AllowedTools for surgical removal.
	DisallowedTools []string `yaml:"disallowed_tools,omitempty"`
}

// TelegramChannelConfig holds Telegram-specific channel configuration.
type TelegramChannelConfig struct {
	// Token is the Telegram bot token from @BotFather.
	// Supports secret references: ${secret:NAME}.
	Token string `yaml:"token"`

	// AllowedUsers restricts which Telegram user IDs can interact with this bot.
	// When non-empty, messages from users not in this list are silently ignored.
	// Find your user ID by messaging @userinfobot on Telegram.
	AllowedUsers []int64 `yaml:"allowed_users,omitempty"`
}

// Env identifies the runtime environment.
type Env string

const (
	EnvLocal Env = "local"
	EnvProd  Env = "prod"
)

// IsLocal returns true if this is the local development environment.
func (e Env) IsLocal() bool { return e == EnvLocal }

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

		if u.Role != "" && !u.Role.Valid() {
			return fmt.Errorf("user %q: unknown role %q (known: %v)", u.ID, u.Role, role.ValidRoles())
		}
		if u.Role != "" && len(u.AllowedTools) > 0 {
			return fmt.Errorf("user %q: role and allowed_tools are mutually exclusive", u.ID)
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

			switch ch.Type {
			case ChannelTypeSocket, ChannelTypeStdio:
				// valid
			case ChannelTypeTelegram:
				if ch.TelegramConfig == nil || ch.TelegramConfig.Token == "" {
					return fmt.Errorf("user %q channel %q: telegram channel requires telegram.token", u.ID, ch.Name)
				}
			case "":
				return fmt.Errorf("user %q channel %q: missing type", u.ID, ch.Name)
			default:
				return fmt.Errorf("user %q channel %q: unknown type %q (known: socket, stdio, telegram)", u.ID, ch.Name, ch.Type)
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
			tc := cfg.Users[i].Channels[j].TelegramConfig
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

	// Resolve provider credentials.
	if cfg.Providers.Google != nil {
		val, envVar, err := resolveRef(cfg.Providers.Google.ClientID)
		if err != nil {
			return nil, fmt.Errorf("providers.google.client_id: %w", err)
		}
		cfg.Providers.Google.ClientID = val
		if envVar != "" {
			envVars = append(envVars, envVar)
		}

		val, envVar, err = resolveRef(cfg.Providers.Google.ClientSecret)
		if err != nil {
			return nil, fmt.Errorf("providers.google.client_secret: %w", err)
		}
		cfg.Providers.Google.ClientSecret = val
		if envVar != "" {
			envVars = append(envVars, envVar)
		}
	}

	if cfg.Providers.Monzo != nil {
		val, envVar, err := resolveRef(cfg.Providers.Monzo.ClientID)
		if err != nil {
			return nil, fmt.Errorf("providers.monzo.client_id: %w", err)
		}
		cfg.Providers.Monzo.ClientID = val
		if envVar != "" {
			envVars = append(envVars, envVar)
		}

		val, envVar, err = resolveRef(cfg.Providers.Monzo.ClientSecret)
		if err != nil {
			return nil, fmt.Errorf("providers.monzo.client_secret: %w", err)
		}
		cfg.Providers.Monzo.ClientSecret = val
		if envVar != "" {
			envVars = append(envVars, envVar)
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

// resolveSecret tries the OS keychain first, then falls back to env var.
// Returns the resolved value and the env var name if one was used.
func resolveSecret(name string) (string, string, error) {
	if secret.KeychainAvailable() {
		ks := secret.NewKeychainStore("_config")
		val, err := ks.Get(context.Background(), name)
		if err != nil {
			return "", "", fmt.Errorf("keychain lookup %q: %w", name, err)
		}
		if val != "" {
			slog.Info("resolved secret from keychain", "name", name)
			return val, "", nil
		}
	}

	// Fall back to env var with the same name.
	val := os.Getenv(name)
	if val != "" {
		slog.Info("resolved secret from env var", "name", name)
		return val, name, nil
	}

	return "", "", fmt.Errorf("secret %q not found in keychain or env var", name)
}

// ToUserConfig converts a config User to a user.Config (without system-derived fields).
func (u *User) ToUserConfig() user.Config {
	return user.Config{
		ID:              u.ID,
		APIKey:          u.APIKey,
		Model:           u.Model,
		PermissionMode:  u.PermissionMode,
		Role:            u.Role,
		AllowedTools:    u.AllowedTools,
		DisallowedTools: u.DisallowedTools,
		MaxTurns:        u.MaxTurns,
		Debug:           u.Debug,
		SystemPrompt:    u.SystemPrompt,
	}
}
