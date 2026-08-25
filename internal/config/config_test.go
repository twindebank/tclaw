package config

import (
	"testing"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/repo"

	"github.com/stretchr/testify/require"
)

// validConfig returns a minimal config that passes validation.
func validConfig() *Config {
	return &Config{
		Users: []User{
			{
				ID: "testuser",
				Channels: []Channel{
					{
						Type:        channel.TypeSocket,
						Name:        "main",
						Description: "primary channel",
					},
				},
			},
		},
	}
}

func TestValidate_ValidMinimalConfig(t *testing.T) {
	err := validate(validConfig())
	require.NoError(t, err)
}

func TestValidate_NoUsers(t *testing.T) {
	cfg := &Config{}
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no users defined")
}

func TestValidate_EmptyUserID(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].ID = ""
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing id")
}

func TestValidate_DuplicateUserID(t *testing.T) {
	cfg := validConfig()
	cfg.Users = append(cfg.Users, cfg.Users[0])
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate id")
}

func TestValidate_NoChannels(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].Channels = nil
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no channels defined")
}

func TestValidate_EmptyChannelName(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].Channels[0].Name = ""
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing name")
}

func TestValidate_InvalidChannelName(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].Channels[0].Name = "../path"
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid characters")
}

func TestValidate_DuplicateChannelName(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].Channels = append(cfg.Users[0].Channels, Channel{
		Type:        channel.TypeSocket,
		Name:        "main",
		Description: "duplicate",
	})
	err := validate(cfg)
	// Duplicates are silently dropped rather than causing a fatal error —
	// crashing on startup makes it impossible to SSH in and fix the config.
	require.NoError(t, err)
	require.Len(t, cfg.Users[0].Channels, 1, "duplicate should be removed")
}

func TestValidate_EmptyChannelDescription(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].Channels[0].Description = ""
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing description")
}

func TestValidate_ChannelModel(t *testing.T) {
	t.Run("known model is accepted", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Channels[0].Model = "claude-opus-4-8"
		require.NoError(t, validate(cfg))
	})

	t.Run("unknown model is rejected", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Channels[0].Model = "claude-opus-9-9"
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown model")
	})

	t.Run("empty model is accepted (inherits user-level)", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Channels[0].Model = ""
		require.NoError(t, validate(cfg))
	})
}

func TestValidate_ChannelMaxTurns(t *testing.T) {
	t.Run("positive limit is accepted", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Channels[0].MaxTurns = 100
		require.NoError(t, validate(cfg))
	})

	t.Run("zero is accepted (inherits user-level)", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Channels[0].MaxTurns = 0
		require.NoError(t, validate(cfg))
	})

	t.Run("negative limit is rejected", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Channels[0].MaxTurns = -1
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "max_turns must be zero (inherit) or positive")
	})
}

func TestValidate_MissingChannelType(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].Channels[0].Type = ""
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing type")
}

func TestValidate_UnknownChannelType(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].Channels[0].Type = "carrier_pigeon"
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown type")
}

func TestValidate_TelegramWithoutUserID(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].Channels[0].Type = channel.TypeTelegram
	cfg.Users[0].Channels[0].Telegram = &TelegramChannelConfig{Token: "fake-token"}
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "telegram.user_id")
}

func TestValidate_TelegramValid(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].Telegram = &UserTelegramConfig{UserID: "123456"}
	cfg.Users[0].Channels[0].Type = channel.TypeTelegram
	cfg.Users[0].Channels[0].Telegram = &TelegramChannelConfig{Token: "fake-token"}
	err := validate(cfg)
	require.NoError(t, err)
}

func TestValidate_ClaudeSessionTimeout(t *testing.T) {
	t.Run("accepts valid duration", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Channels[0].ClaudeSessionTimeout = "10m"
		require.NoError(t, validate(cfg))
	})

	t.Run("accepts empty (no timeout)", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Channels[0].ClaudeSessionTimeout = ""
		require.NoError(t, validate(cfg))
	})

	t.Run("rejects unparseable duration", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Channels[0].ClaudeSessionTimeout = "not a duration"
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid claude_session_timeout")
	})
}

func TestValidate_MessageDebounce(t *testing.T) {
	t.Run("accepts a valid duration", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].MessageDebounce = "2s"
		require.NoError(t, validate(cfg))
	})

	t.Run("accepts empty (defaults applied later)", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].MessageDebounce = ""
		require.NoError(t, validate(cfg))
	})

	t.Run("rejects unparseable duration", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].MessageDebounce = "not a duration"
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid message_debounce")
	})
}

func TestUser_MessageDebounceDuration(t *testing.T) {
	t.Run("defaults to 1s when unset", func(t *testing.T) {
		u := User{ID: "u"}
		require.Equal(t, defaultMessageDebounce, u.ToUserConfig().MessageDebounce)
	})

	t.Run("parses an explicit duration", func(t *testing.T) {
		u := User{ID: "u", MessageDebounce: "3s"}
		require.Equal(t, 3*time.Second, u.ToUserConfig().MessageDebounce)
	})

	t.Run("treats 0s as explicitly disabled", func(t *testing.T) {
		u := User{ID: "u", MessageDebounce: "0s"}
		require.Equal(t, time.Duration(0), u.ToUserConfig().MessageDebounce)
	})
}

func TestValidate_Knowledge(t *testing.T) {
	withVault := func(access repo.Access) *Config {
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{{Name: "knowledge", Repo: "owner/personal-knowledge", Access: access}}
		cfg.Users[0].Knowledge = &KnowledgeConfig{Repo: "knowledge"}
		return cfg
	}

	t.Run("resolves the reference and mounts the vault at its default path", func(t *testing.T) {
		cfg := withVault(repo.AccessFullWrite)
		require.NoError(t, validate(cfg))

		require.Equal(t, defaultKnowledgeMountAt, cfg.Users[0].Knowledge.MountAt)
		require.Equal(t, defaultKnowledgeMountAt, cfg.Users[0].Repos[0].MountAt,
			"the vault repo should clone to the knowledge path, not under repos/")
	})

	t.Run("rejects a reference to no declared repo", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Knowledge = &KnowledgeConfig{Repo: "not-declared"}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not match any repos entry")
	})

	t.Run("rejects a vault that cannot push", func(t *testing.T) {
		// The agent commits and pushes the vault every turn, so a read-only
		// tier would leave it silently failing to save.
		cfg := withVault(repo.AccessReadOnly)
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "must be able to push")
	})

	t.Run("rejects an empty repo reference", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Knowledge = &KnowledgeConfig{Repo: "  "}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "repo is required")
	})

	t.Run("accepts vault directories to install into the Claude config dir", func(t *testing.T) {
		cfg := withVault(repo.AccessFullWrite)
		cfg.Users[0].Knowledge.ClaudeDirs = map[string]string{
			"skills":        "vault-claude/skills",
			"agents":        "vault-claude/agents",
			"patterns":      "vault-claude/rules",
			"output-styles": "vault-claude/output-styles",
		}
		require.NoError(t, validate(cfg))
	})

	t.Run("rejects a source path that climbs out of the vault", func(t *testing.T) {
		// The source is joined onto the clone, so a path that escapes it would
		// install files from anywhere on the volume.
		escapes := map[string]string{
			"a parent path":    "../../secrets",
			"an absolute path": "/data/tclaw",
			"an unclean path":  "vault-claude/../../etc",
			"a trailing slash": "vault-claude/skills/",
			"an empty path":    "",
		}
		for name, dir := range escapes {
			t.Run(name, func(t *testing.T) {
				cfg := withVault(repo.AccessFullWrite)
				cfg.Users[0].Knowledge.ClaudeDirs = map[string]string{"skills": dir}
				err := validate(cfg)
				require.Error(t, err)
				require.Contains(t, err.Error(), "must be a plain path inside the vault")
			})
		}
	})

	t.Run("rejects a target name that is not a single directory", func(t *testing.T) {
		// The name is joined onto the agent's own Claude directory, so anything
		// with a separator in it would write outside that directory.
		for _, name := range []string{"", "..", ".", "skills/nested", `skills\nested`} {
			cfg := withVault(repo.AccessFullWrite)
			cfg.Users[0].Knowledge.ClaudeDirs = map[string]string{name: "vault-claude/skills"}
			err := validate(cfg)
			require.Error(t, err, "name %q", name)
			require.Contains(t, err.Error(), "must be a single directory name")
		}
	})
}

func TestValidate_CredentialSlots(t *testing.T) {
	t.Run("accepts a slot with no fields", func(t *testing.T) {
		// Declaring a slot without a value is the whole point: it can be
		// referenced now and filled from a phone later.
		cfg := validConfig()
		cfg.Users[0].Repos = nil
		cfg.CredentialSlots = []CredentialSlot{{
			Type:        "git",
			Label:       "homeassistant",
			Description: "Scoped PAT",
		}}
		require.NoError(t, validate(cfg))
	})

	t.Run("accepts channel scoping that names a declared channel", func(t *testing.T) {
		cfg := validConfig()
		cfg.CredentialSlots = []CredentialSlot{{Type: "google", Label: "work", Channel: "main"}}
		require.NoError(t, validate(cfg))
	})

	t.Run("rejects channel scoping that names an unknown channel", func(t *testing.T) {
		cfg := validConfig()
		cfg.CredentialSlots = []CredentialSlot{{Type: "google", Label: "work", Channel: "nope"}}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), `channel "nope" does not match any channel name`)
	})

	t.Run("rejects duplicate slots", func(t *testing.T) {
		cfg := validConfig()
		cfg.CredentialSlots = []CredentialSlot{
			{Type: "git", Label: "default"},
			{Type: "git", Label: "default"},
		}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), `duplicate slot "git/default"`)
	})

	t.Run("rejects a type or label that is unsafe in a store key", func(t *testing.T) {
		cfg := validConfig()
		cfg.CredentialSlots = []CredentialSlot{{Type: "git/../etc", Label: "default"}}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "type")

		cfg.CredentialSlots = []CredentialSlot{{Type: "git", Label: "../escape"}}
		err = validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "label")
	})
}

func TestValidateCredentialSlotTypes(t *testing.T) {
	t.Run("accepts a known type", func(t *testing.T) {
		require.NoError(t, ValidateCredentialSlotTypes(
			[]CredentialSlot{{Type: "git", Label: "default"}},
			[]string{"google", "git"},
		))
	})

	t.Run("rejects a type nothing consumes and lists the known ones", func(t *testing.T) {
		err := ValidateCredentialSlotTypes(
			[]CredentialSlot{{Type: "gti", Label: "default"}},
			[]string{"google", "git"},
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), `unknown type "gti"`)
		require.Contains(t, err.Error(), "google, git")
	})
}

func TestValidate_Repos(t *testing.T) {
	t.Run("expands owner/repo shorthand and defaults the branch", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{{Name: "ha-config", Repo: "owner/homeassistant-config"}}
		require.NoError(t, validate(cfg))

		require.Equal(t, "https://github.com/owner/homeassistant-config", cfg.Users[0].Repos[0].Repo)
		require.Equal(t, "main", cfg.Users[0].Repos[0].Branch)
	})

	t.Run("accepts channel scoping that names a declared channel", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{{
			Name:              "ha-config",
			Repo:              "owner/homeassistant-config",
			VisibleToChannels: []string{"main"},
		}}
		require.NoError(t, validate(cfg))
	})

	t.Run("rejects channel scoping that names an unknown channel", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{{
			Name:              "ha-config",
			Repo:              "owner/homeassistant-config",
			VisibleToChannels: []string{"nope"},
		}}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), `"nope" does not match any channel name`)
	})

	t.Run("rejects a missing repo", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{{Name: "ha-config"}}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "repo is required")
	})

	t.Run("rejects a name that isn't a safe directory name", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{{Name: "../escape", Repo: "owner/repo"}}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "alphanumeric/hyphens")
	})

	t.Run("rejects duplicate names", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{
			{Name: "ha-config", Repo: "owner/one"},
			{Name: "ha-config", Repo: "owner/two"},
		}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "duplicate name")
	})

	t.Run("defaults access to read_only", func(t *testing.T) {
		// Least privilege by default: push access is only ever explicit.
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{{Name: "ha-config", Repo: "owner/repo"}}
		require.NoError(t, validate(cfg))
		require.Equal(t, repo.AccessReadOnly, cfg.Users[0].Repos[0].Access)
	})

	t.Run("rejects an unknown access tier", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{{Name: "ha-config", Repo: "owner/repo", Access: "write_everything"}}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown access")
	})

	t.Run("accepts a credential naming a declared git slot", func(t *testing.T) {
		cfg := validConfig()
		cfg.CredentialSlots = []CredentialSlot{{Type: "git", Label: "homeassistant"}}
		cfg.Users[0].Repos = []RepoConfig{{Name: "ha-config", Repo: "owner/repo", Credential: "homeassistant"}}
		require.NoError(t, validate(cfg))
	})

	t.Run("rejects a credential with no matching slot", func(t *testing.T) {
		// Otherwise this fails much later as an unhelpful auth error at fetch time.
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{{Name: "ha-config", Repo: "owner/repo", Credential: "nope"}}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not match any credential_slots entry")
	})

	t.Run("parses the lifecycle timings", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{{
			Name:                 "ha-config",
			Repo:                 "owner/repo",
			FetchEvery:           "6h",
			DropToReadOnlyAt:     "2026-12-01T00:00:00Z",
			DropCloneIfUnusedFor: "2160h",
		}}
		require.NoError(t, validate(cfg))

		got := cfg.Users[0].ToUserConfig().Repos[0]
		require.Equal(t, 6*time.Hour, got.FetchEvery)
		require.Equal(t, 2160*time.Hour, got.DropCloneIfUnusedFor)
		require.Equal(t, 2026, got.DropToReadOnlyAt.Year())
	})

	t.Run("rejects malformed lifecycle timings", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{{Name: "r", Repo: "owner/repo", FetchEvery: "soon"}}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "fetch_every")

		cfg.Users[0].Repos = []RepoConfig{{Name: "r", Repo: "owner/repo", DropToReadOnlyAt: "next tuesday"}}
		err = validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "drop_to_read_only_at")
	})
}

func TestUser_ToUserConfig_Repos(t *testing.T) {
	t.Run("carries declared repos through to the user config", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Repos = []RepoConfig{{
			Name:              "ha-config",
			Repo:              "owner/homeassistant-config",
			Description:       "Home Assistant config mirror",
			VisibleToChannels: []string{"main"},
		}}
		require.NoError(t, validate(cfg))

		got := cfg.Users[0].ToUserConfig()
		require.Len(t, got.Repos, 1)
		require.Equal(t, "ha-config", got.Repos[0].Name)
		require.Equal(t, "https://github.com/owner/homeassistant-config", got.Repos[0].URL)
		require.Equal(t, "main", got.Repos[0].Branch)
		require.Equal(t, "Home Assistant config mirror", got.Repos[0].Description)
		require.Equal(t, []string{"main"}, got.Repos[0].Channels)
	})
}
