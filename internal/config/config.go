// Package config handles YAML configuration loading, validation, and secret resolution. Secrets
// referenced as ${secret:NAME} are resolved from the OS keychain or environment variables.
// config.Writer provides atomic read-modify-write mutations (temp file + rename) for runtime
// changes made by channel and config tools.
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

	"tclaw/internal/channel"
	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/toolgroup"
	"tclaw/internal/user"

	"gopkg.in/yaml.v3"
)

// channelNamePattern restricts channel names to safe characters only.
// Prevents path traversal when names are used in filesystem paths or URL routes.
var channelNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// defaultMessageDebounce is applied when a user leaves message_debounce unset, so
// bursts (e.g. a photo album delivered as separate messages) coalesce into one
// turn by default. Set message_debounce: "0s" to opt out.
const defaultMessageDebounce = 1 * time.Second

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

	// MessageDebounce coalesces same-channel user messages that arrive within
	// this rolling window into a single agent turn (e.g. a photo album delivered
	// as separate messages). A duration string like "1s"; unset defaults to 1s,
	// "0s" disables debouncing.
	MessageDebounce string `yaml:"message_debounce,omitempty"`

	AllowedTools    []claudecli.Tool `yaml:"allowed_tools"`
	DisallowedTools []claudecli.Tool `yaml:"disallowed_tools"`
	SystemPrompt    string           `yaml:"system_prompt"`

	// Knowledge, when set, mounts a git-backed personal knowledge base that the
	// agent reads from and writes to as its durable knowledge store.
	Knowledge *KnowledgeConfig `yaml:"knowledge,omitempty"`

	// Repos declares git repositories cloned on boot and mounted read-only for
	// the agent to browse. Optional per-repo channel scoping keeps a repo
	// visible only to the channels that need it.
	Repos []RepoConfig `yaml:"repos,omitempty"`

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

// KnowledgeConfig configures the personal knowledge base integration. The vault
// is cloned per-user and exposed to the agent as a writable git repo; pushes are
// authenticated server-side (see internal/knowledgeproxy) so the token never
// reaches the agent subprocess. Auth reuses the shared "github_token" secret.
type KnowledgeConfig struct {
	// Repo is the vault repository. Accepts "owner/repo" shorthand (expanded to
	// https://github.com/owner/repo) or a full HTTPS URL.
	Repo string `yaml:"repo"`

	// Branch is the branch the agent reads and pushes to. Defaults to "main".
	Branch string `yaml:"branch,omitempty"`

	// CommitName and CommitEmail set the git identity used for agent commits.
	// Optional; a tclaw noreply identity is used when empty.
	CommitName  string `yaml:"commit_name,omitempty"`
	CommitEmail string `yaml:"commit_email,omitempty"`
}

// normalize fills defaults and expands "owner/repo" shorthand into a full HTTPS
// URL. Mutates the receiver in place.
func (k *KnowledgeConfig) normalize() error {
	if strings.TrimSpace(k.Repo) == "" {
		return fmt.Errorf("repo is required")
	}
	if k.Branch == "" {
		k.Branch = "main"
	}
	k.Repo = normalizeRepoURL(k.Repo)
	return nil
}

// normalizeRepoURL expands an "owner/repo" shorthand to a github.com HTTPS URL,
// leaving explicit http(s) URLs untouched.
func normalizeRepoURL(repo string) string {
	repo = strings.TrimSpace(repo)
	if strings.HasPrefix(repo, "http://") || strings.HasPrefix(repo, "https://") {
		return repo
	}
	return "https://github.com/" + strings.TrimSuffix(strings.TrimPrefix(repo, "/"), "/")
}

// RepoConfig declares a git repository cloned on boot into <user>/repos/<name>
// and mounted read-only in the agent's sandbox. The clone is a mirror the agent
// browses — it is reset to the remote on every sync, so local edits never
// survive. Runtime-added repos (repo_add) use the same directory and tools;
// declaring one here just makes it survive restarts and volume wipes.
type RepoConfig struct {
	// Name is the directory alias under <user>/repos/ and the handle the repo
	// tools take. Alphanumeric and hyphens only.
	Name string `yaml:"name"`

	// Repo accepts "owner/repo" shorthand (expanded to a github.com HTTPS URL)
	// or a full HTTPS URL. Private repos authenticate with the shared
	// "github_token" secret.
	Repo string `yaml:"repo"`

	// Branch is the branch to track. Defaults to "main".
	Branch string `yaml:"branch,omitempty"`

	// Description tells the agent what the repo is for. Surfaced by repo_list.
	Description string `yaml:"description,omitempty"`

	// Channels restricts the repo to the named channels — its clone is only
	// mounted for turns on those channels and the repo tools refuse to touch it
	// elsewhere. Empty means every channel sees it.
	Channels []string `yaml:"channels,omitempty"`
}

// validRepoName matches the directory alias a repo is cloned into. Kept in sync
// with the equivalent check in repotools so config-declared and agent-added
// repos accept the same names.
var validRepoName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// normalize fills defaults and expands "owner/repo" shorthand into a full HTTPS
// URL. Mutates the receiver in place.
func (r *RepoConfig) normalize() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if !validRepoName.MatchString(r.Name) || len(r.Name) > 64 {
		return fmt.Errorf("name %q must be alphanumeric/hyphens, max 64 chars", r.Name)
	}
	if strings.TrimSpace(r.Repo) == "" {
		return fmt.Errorf("repo %q: repo is required", r.Name)
	}
	if r.Branch == "" {
		r.Branch = "main"
	}
	r.Repo = normalizeRepoURL(r.Repo)
	return nil
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

	// Model overrides the user-level model for turns on this channel. Empty
	// means "inherit" — fall back to a runtime override (set via the model
	// tools) or the user-level model, in that order. Lets each channel pin its
	// own model (e.g. Opus for admin work, Sonnet for lightweight monitoring).
	Model claudecli.Model `yaml:"model,omitempty"`

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

	// ClaudeSessionTimeout caps how long a Claude CLI session can sit idle
	// before the next incoming message starts a fresh one (no --resume),
	// keeping the context window bounded on long-lived channels like email.
	// Parsed as a Go duration string (e.g. "10m", "1h"). Empty or zero means
	// "no timeout — sessions live until explicitly reset".
	ClaudeSessionTimeout string `yaml:"claude_session_timeout,omitempty"`
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

		if u.MessageDebounce != "" {
			if _, err := time.ParseDuration(u.MessageDebounce); err != nil {
				return fmt.Errorf("user %q: invalid message_debounce %q: %w", u.ID, u.MessageDebounce, err)
			}
		}

		// Knowledge is a pointer shared with cfg.Users[i], so normalizing through
		// u.Knowledge also updates the stored config.
		if u.Knowledge != nil {
			if err := u.Knowledge.normalize(); err != nil {
				return fmt.Errorf("user %q knowledge: %w", u.ID, err)
			}
		}

		if len(u.Channels) == 0 {
			return fmt.Errorf("user %q: no channels defined", u.ID)
		}

		// Deduplicate channels — a previous bug could leave duplicate entries in the
		// config file, and crashing on startup makes it impossible to SSH in and fix.
		{
			deduped := make([]Channel, 0, len(u.Channels))
			seen := make(map[string]bool, len(u.Channels))
			for _, ch := range u.Channels {
				if seen[ch.Name] {
					slog.Warn("dropping duplicate channel from config",
						"user", u.ID, "channel", ch.Name)
					continue
				}
				seen[ch.Name] = true
				deduped = append(deduped, ch)
			}
			if len(deduped) < len(u.Channels) {
				cfg.Users[i].Channels = deduped
				u = cfg.Users[i]
			}
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

			if ch.Model != "" && !claudecli.ValidModel(ch.Model) {
				return fmt.Errorf("user %q channel %q: unknown model %q (known: %v)", u.ID, ch.Name, ch.Model, claudecli.ValidModels())
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

			if ch.ClaudeSessionTimeout != "" {
				if _, err := time.ParseDuration(ch.ClaudeSessionTimeout); err != nil {
					return fmt.Errorf("user %q channel %q: invalid claude_session_timeout %q: %w", u.ID, ch.Name, ch.ClaudeSessionTimeout, err)
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

		// Repos normalize in place — u.Repos shares its backing array with
		// cfg.Users[i].Repos, so the expanded URLs and defaults are stored.
		// Validated after channels so channel scoping can be checked against
		// the declared channel names.
		repoNames := make(map[string]bool, len(u.Repos))
		for j := range u.Repos {
			r := &u.Repos[j]
			if err := r.normalize(); err != nil {
				return fmt.Errorf("user %q repos[%d]: %w", u.ID, j, err)
			}
			if repoNames[r.Name] {
				return fmt.Errorf("user %q repos[%d]: duplicate name %q", u.ID, j, r.Name)
			}
			repoNames[r.Name] = true
			for k, chName := range r.Channels {
				if !chNames[chName] {
					return fmt.Errorf("user %q repo %q channels[%d]: %q does not match any channel name", u.ID, r.Name, k, chName)
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
	var tgUserID string
	if u.Telegram != nil {
		tgUserID = u.Telegram.UserID
	}
	var knowledge *user.Knowledge
	if u.Knowledge != nil {
		knowledge = &user.Knowledge{
			Repo:        u.Knowledge.Repo,
			Branch:      u.Knowledge.Branch,
			CommitName:  u.Knowledge.CommitName,
			CommitEmail: u.Knowledge.CommitEmail,
		}
	}
	var repos []user.Repo
	for _, r := range u.Repos {
		repos = append(repos, user.Repo{
			Name:        r.Name,
			URL:         r.Repo,
			Branch:      r.Branch,
			Description: r.Description,
			Channels:    r.Channels,
		})
	}
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
		TelegramUserID:  tgUserID,
		Knowledge:       knowledge,
		Repos:           repos,
		MessageDebounce: u.messageDebounceDuration(),
	}
}

// messageDebounceDuration resolves the configured debounce window. An unset
// (empty) value applies the 1s default; a parse error — already rejected by
// validate before ToUserConfig runs — falls back to the default rather than
// silently disabling debouncing.
func (u *User) messageDebounceDuration() time.Duration {
	if u.MessageDebounce == "" {
		return defaultMessageDebounce
	}
	d, err := time.ParseDuration(u.MessageDebounce)
	if err != nil {
		slog.Warn("config: invalid message_debounce, using default",
			"user", u.ID, "value", u.MessageDebounce, "err", err)
		return defaultMessageDebounce
	}
	return d
}
