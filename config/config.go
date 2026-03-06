package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/user"

	"gopkg.in/yaml.v3"
)

// File is the top-level config file structure.
type File struct {
	// BaseDir is the root for all per-user data (home dirs, stores).
	// Defaults to /tmp/tclaw if not set.
	BaseDir string `yaml:"base_dir"`

	// OAuth configures the OAuth callback server for provider authorization.
	OAuth OAuthConfig `yaml:"oauth"`

	// Providers configures external service providers (Gmail, etc.).
	Providers ProvidersConfig `yaml:"providers"`

	Users []User `yaml:"users"`
}

// OAuthConfig holds settings for the OAuth callback server.
type OAuthConfig struct {
	// CallbackAddr is the address for the OAuth callback HTTP server.
	// Defaults to "127.0.0.1:9876".
	CallbackAddr string `yaml:"callback_addr"`
}

// ProvidersConfig holds per-provider configuration.
type ProvidersConfig struct {
	Gmail *ProviderCredentials `yaml:"gmail"`
}

// ProviderCredentials holds OAuth client credentials for a provider.
type ProviderCredentials struct {
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
	AllowedTools   []claudecli.Tool         `yaml:"allowed_tools"`
	DisallowedTools []claudecli.Tool        `yaml:"disallowed_tools"`
	SystemPrompt   string                   `yaml:"system_prompt"`

	Channels []Channel `yaml:"channels"`
}

// Channel defines a channel attached to a user.
// Type, Name, and Description are required; other fields depend on the transport.
type Channel struct {
	Type        ChannelType `yaml:"type"`
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`

	// Telegram-specific (future)
	Token string `yaml:"token,omitempty"`
}

// ChannelType identifies the transport kind in config.
type ChannelType string

const (
	ChannelTypeSocket ChannelType = "socket"
	ChannelTypeStdio  ChannelType = "stdio"
)

// Load reads and parses a config file from the given path.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg File
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.BaseDir == "" {
		cfg.BaseDir = "/tmp/tclaw"
	}
	if cfg.OAuth.CallbackAddr == "" {
		cfg.OAuth.CallbackAddr = "127.0.0.1:9876"
	}

	if err := resolveSecrets(&cfg); err != nil {
		return nil, fmt.Errorf("resolve secrets: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func validate(cfg *File) error {
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
			if chNames[ch.Name] {
				return fmt.Errorf("user %q channel %d: duplicate name %q", u.ID, j, ch.Name)
			}
			chNames[ch.Name] = true

			if ch.Description == "" {
				return fmt.Errorf("user %q channel %q: missing description", u.ID, ch.Name)
			}

			switch ch.Type {
			case ChannelTypeSocket, ChannelTypeStdio:
				// valid
			case "":
				return fmt.Errorf("user %q channel %q: missing type", u.ID, ch.Name)
			default:
				return fmt.Errorf("user %q channel %q: unknown type %q (known: socket, stdio)", u.ID, ch.Name, ch.Type)
			}
		}
	}

	return nil
}

const (
	secretRefPrefix = "${secret:"
	envRefPrefix    = "${"
	refSuffix       = "}"
)

// resolveSecrets expands secret references in config fields.
//
// Supported syntax:
//
//	${secret:NAME}  — tries OS keychain for NAME, falls back to env var NAME
//	${NAME}         — env var only (legacy syntax)
//	literal         — used as-is
func resolveSecrets(cfg *File) error {
	for i := range cfg.Users {
		val, err := resolveRef(cfg.Users[i].APIKey)
		if err != nil {
			return fmt.Errorf("user %q api_key: %w", cfg.Users[i].ID, err)
		}
		cfg.Users[i].APIKey = val
	}

	// Resolve provider credentials.
	if cfg.Providers.Gmail != nil {
		val, err := resolveRef(cfg.Providers.Gmail.ClientID)
		if err != nil {
			return fmt.Errorf("providers.gmail.client_id: %w", err)
		}
		cfg.Providers.Gmail.ClientID = val

		val, err = resolveRef(cfg.Providers.Gmail.ClientSecret)
		if err != nil {
			return fmt.Errorf("providers.gmail.client_secret: %w", err)
		}
		cfg.Providers.Gmail.ClientSecret = val
	}

	return nil
}

func resolveRef(s string) (string, error) {
	if !strings.HasPrefix(s, envRefPrefix) || !strings.HasSuffix(s, refSuffix) {
		return s, nil
	}

	inner := s[2 : len(s)-1] // strip ${ and }

	// ${secret:NAME} — keychain first, env var fallback
	if name, ok := strings.CutPrefix(inner, "secret:"); ok {
		return resolveSecret(name)
	}

	// ${NAME} — env var only
	val := os.Getenv(inner)
	if val == "" {
		return "", fmt.Errorf("env var %q is not set", inner)
	}
	return val, nil
}

// resolveSecret tries the OS keychain first, then falls back to env var.
func resolveSecret(name string) (string, error) {
	if secret.KeychainAvailable() {
		ks := secret.NewKeychainStore("_config")
		val, err := ks.Get(context.Background(), name)
		if err != nil {
			return "", fmt.Errorf("keychain lookup %q: %w", name, err)
		}
		if val != "" {
			slog.Info("resolved secret from keychain", "name", name)
			return val, nil
		}
	}

	// Fall back to env var with the same name.
	val := os.Getenv(name)
	if val != "" {
		slog.Info("resolved secret from env var", "name", name)
		return val, nil
	}

	return "", fmt.Errorf("secret %q not found in keychain or env var", name)
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
