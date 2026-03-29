package bankingtools

import (
	"context"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// Package implements toolpkg.Package for Open Banking tools.
type Package struct{}

func (p *Package) Name() string { return "banking" }
func (p *Package) Description() string {
	return "Open Banking (PSD2) via Enable Banking: connect bank accounts, view balances and transactions across multiple UK banks."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupPersonalServices }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__banking_*"}
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
		SecretStore: regCtx.SecretStore,
		StateStore:  regCtx.StateStore,
		Callback:    regCtx.Callback,
		OnCredentialsStored: func() {
			// Register operational tools when credentials become available.
			RegisterTools(handler, Deps{
				SecretStore: regCtx.SecretStore,
				StateStore:  regCtx.StateStore,
				Callback:    regCtx.Callback,
			})
		},
	}

	// Always register info/setup tools.
	RegisterInfoTools(handler, deps)

	// Register operational tools if credentials already configured.
	if hasBankingCredentials(context.Background(), regCtx.SecretStore) {
		RegisterTools(handler, deps)
	}

	return nil
}

func hasBankingCredentials(ctx context.Context, store secret.Store) bool {
	appID, _ := store.Get(ctx, ApplicationIDStoreKey)
	privKey, _ := store.Get(ctx, PrivateKeyStoreKey)
	return appID != "" && privKey != ""
}
