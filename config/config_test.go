package config

import (
	"testing"

	"tclaw/channel"
	"tclaw/claudecli"
	"tclaw/role"

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

func TestValidate_RoleAndAllowedToolsMutuallyExclusive(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].Role = role.Superuser
	cfg.Users[0].AllowedTools = []claudecli.Tool{"Bash"}
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "role and allowed_tools are mutually exclusive")
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
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate name")
}

func TestValidate_EmptyChannelDescription(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].Channels[0].Description = ""
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing description")
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

func TestValidate_TelegramWithoutToken(t *testing.T) {
	cfg := validConfig()
	cfg.Users[0].Channels[0].Type = channel.TypeTelegram
	cfg.Users[0].Channels[0].TelegramConfig = nil
	err := validate(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "telegram channel requires telegram.token")
}
