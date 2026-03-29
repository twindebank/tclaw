package google

import (
	"context"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// Package implements toolpkg.Package and toolpkg.CredentialProvider for
// Google Workspace tools.
//
// Currently, tools are still registered dynamically per-connection by the
// router's OnProviderConnect callback. The CredentialProvider interface
// declares the OAuth config so it's owned by this package rather than
// provider/google.go.
type Package struct{}

func (p *Package) Name() string { return "google" }
func (p *Package) Description() string {
	return "Google Workspace: Gmail (list, read, send), Calendar (list, create), and workspace data access. Requires an OAuth connection via connection_add."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupGSuiteWrite }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__google_*"}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec {
	// Google uses OAuth connections, not direct secrets.
	return nil
}

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Google Workspace full access."},
		Credentials: nil,
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, regCtx toolpkg.RegistrationContext) error {
	// No-op: Google tools are registered dynamically per-connection by the
	// router's OnProviderConnect callback. This package provides metadata
	// and info tool only.
	return nil
}

// CredentialSpec implements toolpkg.CredentialProvider. Google requires OAuth2
// with client_id/client_secret from the tclaw config.
func (p *Package) CredentialSpec() toolpkg.CredentialSpec {
	return toolpkg.CredentialSpec{
		AuthType: toolpkg.AuthOAuth2,
		Fields: []toolpkg.CredentialField{
			{Key: "client_id", Label: "Google OAuth Client ID", Required: true},
			{Key: "client_secret", Label: "Google OAuth Client Secret", Required: true},
		},
		OAuth: &toolpkg.OAuthSpec{
			AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
			Scopes: []string{
				"https://www.googleapis.com/auth/gmail.modify",
				"https://www.googleapis.com/auth/drive",
				"https://www.googleapis.com/auth/calendar",
				"https://www.googleapis.com/auth/documents",
				"https://www.googleapis.com/auth/spreadsheets",
				"https://www.googleapis.com/auth/presentations",
				"https://www.googleapis.com/auth/tasks",
			},
			ExtraParams: map[string]string{
				"access_type": "offline",
				"prompt":      "consent",
			},
			Services: []string{"Gmail", "Google Drive", "Google Calendar", "Google Docs", "Google Sheets", "Google Slides", "Google Tasks"},
		},
		SupportsMultiple: true,
		ConfigKey:        "providers.google",
	}
}

// OnCredentialSetChange implements toolpkg.CredentialProvider. Currently a
// no-op — Google tools are still registered via the old provider/connection
// system. This will be wired up when the router's provider-specific code is
// removed.
func (p *Package) OnCredentialSetChange(handler *mcp.Handler, ctx toolpkg.RegistrationContext, sets []toolpkg.ResolvedCredentialSet) error {
	return nil
}
