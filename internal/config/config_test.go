package config

import (
	"testing"
	"time"

	"tclaw/internal/channel"

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
	t.Run("expands owner/repo shorthand and defaults the branch", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Knowledge = &KnowledgeConfig{Repo: "owner/knowledge-base"}
		require.NoError(t, validate(cfg))

		require.Equal(t, "https://github.com/owner/knowledge-base", cfg.Users[0].Knowledge.Repo)
		require.Equal(t, "main", cfg.Users[0].Knowledge.Branch)
	})

	t.Run("leaves an explicit URL and branch untouched", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Knowledge = &KnowledgeConfig{
			Repo:   "https://github.com/owner/knowledge-base.git",
			Branch: "trunk",
		}
		require.NoError(t, validate(cfg))

		require.Equal(t, "https://github.com/owner/knowledge-base.git", cfg.Users[0].Knowledge.Repo)
		require.Equal(t, "trunk", cfg.Users[0].Knowledge.Branch)
	})

	t.Run("rejects an empty repo", func(t *testing.T) {
		cfg := validConfig()
		cfg.Users[0].Knowledge = &KnowledgeConfig{Repo: "  "}
		err := validate(cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "repo is required")
	})
}
