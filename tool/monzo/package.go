package monzo

import (
	"context"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// Package implements toolpkg.Package and toolpkg.CredentialProvider for
// Monzo banking tools. Credentials are managed via the unified credential
// system — the agent uses credential_add to set up OAuth client credentials
// and complete the authorization flow.
type Package struct{}

func (p *Package) Name() string { return "monzo" }
func (p *Package) Description() string {
	return "Monzo banking: list accounts, get balances, view pots, and list/get transactions. Requires Monzo API client credentials via credential_add."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupPersonalServices }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__monzo_*"}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec { return nil }

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Personal service integrations."},
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, regCtx toolpkg.RegistrationContext) error {
	// No-op: Monzo tools are registered dynamically via OnCredentialSetChange
	// when credential sets with OAuth tokens are available.
	return nil
}

// CredentialSpec implements toolpkg.CredentialProvider.
func (p *Package) CredentialSpec() toolpkg.CredentialSpec {
	return toolpkg.CredentialSpec{
		AuthType: toolpkg.AuthOAuth2,
		Fields: []toolpkg.CredentialField{
			{Key: "client_id", Label: "Monzo Client ID", Description: "OAuth client ID from developers.monzo.com.", Required: true},
			{Key: "client_secret", Label: "Monzo Client Secret", Description: "OAuth client secret from developers.monzo.com.", Required: true},
		},
		OAuth: &toolpkg.OAuthSpec{
			AuthURL:  "https://auth.monzo.com/",
			TokenURL: "https://api.monzo.com/oauth2/token",
			Services: []string{"Monzo Banking"},
		},
		SupportsMultiple: true,
	}
}

// OnCredentialSetChange implements toolpkg.CredentialProvider. Registers or
// unregisters Monzo tools based on which credential sets have OAuth tokens.
func (p *Package) OnCredentialSetChange(handler *mcp.Handler, ctx toolpkg.RegistrationContext, sets []toolpkg.ResolvedCredentialSet) error {
	// TODO: register operational tools for ready credential sets, unregister
	// when no ready sets remain. For now this is a no-op until the full
	// OnCredentialSetChange wiring is implemented in the router.
	return nil
}
