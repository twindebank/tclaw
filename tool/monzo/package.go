package monzo

import (
	"context"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// Package implements toolpkg.Package for Monzo banking tools.
//
// Monzo is a provider-based package: the set_credentials tool is always
// registered, but operational tools are registered dynamically per-connection
// by the router's OnProviderConnect callback. The Package interface handles
// only the always-visible set_credentials tool.
type Package struct {
	RedirectURL string
}

func (p *Package) Name() string { return "monzo" }
func (p *Package) Description() string {
	return "Monzo banking: list accounts, get balances, view pots, and list/get transactions. Requires Monzo API client credentials."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupPersonalServices }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__monzo_*"}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec {
	return []toolpkg.SecretSpec{
		{
			StoreKey:    ClientIDStoreKey,
			Required:    true,
			Label:       "Monzo Client ID",
			Description: "OAuth client ID from developers.monzo.com.",
		},
		{
			StoreKey:    ClientSecretStoreKey,
			Required:    true,
			Label:       "Monzo Client Secret",
			Description: "OAuth client secret from developers.monzo.com.",
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
	// Only register the set_credentials tool here. Operational tools are
	// registered dynamically by the router when a Monzo connection is created.
	RegisterSetCredentialsTool(handler, SetCredentialsDeps{
		SecretStore: regCtx.SecretStore,
		RedirectURL: p.RedirectURL,
		// OnCredentialsStored is handled by the router via Extra callback.
		OnCredentialsStored: getOnCredentialsStored(regCtx),
	})
	return nil
}

func getOnCredentialsStored(regCtx toolpkg.RegistrationContext) func() {
	if fn, ok := regCtx.Extra["monzo_on_credentials_stored"].(func()); ok {
		return fn
	}
	return func() {}
}
