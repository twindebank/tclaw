package telegramclient

import (
	"context"

	"tclaw/internal/channel"
	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

// Package implements toolpkg.Package for Telegram Client API tools.
type Package struct {
	SecretStore  secret.Store
	StateStore   store.Store
	RuntimeState *channel.RuntimeStateStore

	// TelegramUserID is the user's Telegram user ID from config, passed
	// through to the provisioner for auto-start and notification delivery.
	TelegramUserID string

	// Provisioner is set by Register and read by the router to pass to
	// channeltools for auto-provisioning.
	Provisioner channel.EphemeralProvisioner

	// state is set by Register so ChannelHistoryFunc can access the MTProto client.
	state *handlerState
}

func (p *Package) Name() string { return "telegram_client" }
func (p *Package) Description() string {
	return "Telegram Client API (MTProto): authenticate, configure bots via BotFather, manage chats, read message history, search messages."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupTelegramClient }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__telegram_client_*"},
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec {
	return []toolpkg.SecretSpec{
		{
			StoreKey:     APIIDStoreKey,
			EnvVarPrefix: "TELEGRAM_CLIENT_API_ID",
			Required:     true,
			Label:        "Telegram API ID",
			Description:  "API ID from my.telegram.org.",
		},
		{
			StoreKey:     APIHashStoreKey,
			EnvVarPrefix: "TELEGRAM_CLIENT_API_HASH",
			Required:     true,
			Label:        "Telegram API Hash",
			Description:  "API hash from my.telegram.org.",
		},
	}
}

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Telegram Client API (MTProto)."},
		Credentials: toolpkg.CheckCredentialStatus(ctx, secretStore, p.RequiredSecrets()),
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, regCtx toolpkg.RegistrationContext) error {
	provisioner := RegisterTools(handler, Deps{
		SecretStore: p.SecretStore,
		StateStore:  p.StateStore,
	})

	provisioner.TelegramUserID = p.TelegramUserID

	// Store the provisioner on the struct so the router can read it.
	p.Provisioner = provisioner
	// Store the handler state so ChannelHistoryFunc can access the MTProto client.
	p.state = provisioner.state

	return nil
}

// CredentialSpec implements toolpkg.CredentialProvider.
func (p *Package) CredentialSpec() toolpkg.CredentialSpec {
	return toolpkg.CredentialSpec{
		AuthType: toolpkg.AuthAPIKey,
		Fields: []toolpkg.CredentialField{
			{Key: "api_id", Label: "Telegram API ID", Description: "API ID from my.telegram.org.", Required: true, EnvVarPrefix: "TELEGRAM_CLIENT_API_ID"},
			{Key: "api_hash", Label: "Telegram API Hash", Description: "API hash from my.telegram.org.", Required: true, EnvVarPrefix: "TELEGRAM_CLIENT_API_HASH"},
		},
	}
}

// OnCredentialSetChange implements toolpkg.CredentialProvider. Currently a
// no-op — telegram client tools are still registered via the old Register path.
func (p *Package) OnCredentialSetChange(handler *mcp.Handler, ctx toolpkg.RegistrationContext, sets []toolpkg.ResolvedCredentialSet) error {
	return nil
}
