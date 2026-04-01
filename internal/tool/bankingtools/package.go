package bankingtools

import (
	"context"

	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/oauth"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

// Package implements toolpkg.Package for Open Banking tools.
type Package struct {
	SecretStore secret.Store
	StateStore  store.Store
	Callback    *oauth.CallbackServer
}

func (p *Package) Name() string { return "banking" }
func (p *Package) Description() string {
	return "Open Banking (PSD2) via Enable Banking: connect bank accounts, view balances and transactions across multiple UK banks."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupPersonalServices }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__banking_*"},
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec {
	return []toolpkg.SecretSpec{
		{
			StoreKey:     ApplicationIDStoreKey,
			EnvVarPrefix: "ENABLEBANKING_APP_ID",
			Required:     true,
			Label:        "Enable Banking App ID",
			Description:  "Application ID from enablebanking.com.",
		},
		{
			StoreKey:     PrivateKeyStoreKey,
			EnvVarPrefix: "ENABLEBANKING_PRIVATE_KEY",
			Required:     true,
			Label:        "Enable Banking Private Key",
			Description:  "RSA private key PEM from enablebanking.com.",
		},
	}
}

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Personal service integrations."},
		Credentials: toolpkg.CheckCredentialStatus(ctx, secretStore, p.RequiredSecrets()),
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, regCtx toolpkg.RegistrationContext) error {
	deps := Deps{
		SecretStore: p.SecretStore,
		StateStore:  p.StateStore,
		Callback:    p.Callback,
		OnCredentialsStored: func() {
			// Register operational tools when credentials become available.
			RegisterTools(handler, Deps{
				SecretStore: p.SecretStore,
				StateStore:  p.StateStore,
				Callback:    p.Callback,
			})
		},
	}

	// Always register info/setup tools.
	RegisterInfoTools(handler, deps)

	// Register operational tools if credentials already configured.
	if hasBankingCredentials(context.Background(), p.SecretStore) {
		RegisterTools(handler, deps)
	}

	return nil
}

func hasBankingCredentials(ctx context.Context, store secret.Store) bool {
	appID, _ := store.Get(ctx, ApplicationIDStoreKey)
	privKey, _ := store.Get(ctx, PrivateKeyStoreKey)
	return appID != "" && privKey != ""
}

// CredentialSpec implements toolpkg.CredentialProvider.
func (p *Package) CredentialSpec() toolpkg.CredentialSpec {
	return toolpkg.CredentialSpec{
		AuthType: toolpkg.AuthAPIKey,
		Fields: []toolpkg.CredentialField{
			{Key: "app_id", Label: "Enable Banking App ID", Description: "Application ID from enablebanking.com.", Required: true, EnvVarPrefix: "ENABLEBANKING_APP_ID"},
			{Key: "private_key", Label: "Enable Banking Private Key", Description: "RSA private key PEM from enablebanking.com.", Required: true, EnvVarPrefix: "ENABLEBANKING_PRIVATE_KEY"},
		},
	}
}

// OnCredentialSetChange implements toolpkg.CredentialProvider. Currently a
// no-op — banking tools are still registered via the old Register path.
func (p *Package) OnCredentialSetChange(handler *mcp.Handler, ctx toolpkg.RegistrationContext, sets []toolpkg.ResolvedCredentialSet) error {
	return nil
}
