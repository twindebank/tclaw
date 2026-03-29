package telegramclient

import (
	"context"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// ExtraKeyProvisioner is the RegistrationContext.Extra key where the
// Provisioner is stored after registration, so the router can pass it
// to channeltools for auto-provisioning.
const ExtraKeyProvisioner = "telegram_client_provisioner"

// Package implements toolpkg.Package for Telegram Client API tools.
type Package struct{}

func (p *Package) Name() string { return "telegram_client" }
func (p *Package) Description() string {
	return "Telegram Client API (MTProto): authenticate, configure bots via BotFather, manage chats, read message history, search messages."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupTelegramClient }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__telegram_client_*"}
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
		SecretStore: regCtx.SecretStore,
		StateStore:  regCtx.StateStore,
	})

	// Store the provisioner in Extra so the router can pass it to channeltools.
	if regCtx.Extra != nil {
		regCtx.Extra[ExtraKeyProvisioner] = provisioner
	}

	return nil
}
