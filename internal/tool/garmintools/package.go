// Package garmintools exposes Garmin Connect device settings as MCP tools.
//
// The Garmin API work lives in github.com/twindebank/garmin-settings; this package is a thin
// adapter that maps those calls onto MCP tools, keeps the OAuth token in tclaw's encrypted store
// rather than on disk, and holds a pending MFA challenge in memory between turns.
package garmintools

import (
	"context"

	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

// Package implements toolpkg.Package and toolpkg.CredentialProvider for Garmin Connect.
type Package struct {
	SecretStore secret.Store
}

func (p *Package) Name() string { return "garmin" }

func (p *Package) Description() string {
	return "Garmin Connect device settings: read and write units, alerts, sync behaviour, training " +
		"zones and activity data screens on a watch or bike computer. Changes apply the next time " +
		"the device syncs — there is no way to force a sync."
}

func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupPersonalServices }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__garmin_*"},
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec {
	return []toolpkg.SecretSpec{
		{
			StoreKey:     EmailStoreKey,
			EnvVarPrefix: "GARMIN_EMAIL",
			Required:     true,
			Label:        "Garmin Connect email",
			Description:  "The email address for your Garmin Connect account.",
		},
		{
			StoreKey:     PasswordStoreKey,
			EnvVarPrefix: "GARMIN_PASSWORD",
			Required:     true,
			Label:        "Garmin Connect password",
			Description: "Used once to sign in. An OAuth token is stored afterwards and refreshed " +
				"automatically, so the password is not needed for day-to-day use.",
		},
	}
}

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo: toolgroup.GroupInfo{
			Group:       p.Group(),
			Description: "Personal service integrations: TfL transport, restaurant reservations, banking, Monzo.",
		},
		Credentials: toolpkg.CheckCredentialStatus(ctx, secretStore, p.RequiredSecrets()),
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, _ toolpkg.RegistrationContext) error {
	RegisterTools(handler, Deps{SecretStore: p.SecretStore})
	return nil
}

// CredentialSpec implements toolpkg.CredentialProvider. Garmin needs an email and password to sign
// in the first time; everything after that runs on the stored token.
func (p *Package) CredentialSpec() toolpkg.CredentialSpec {
	return toolpkg.CredentialSpec{
		AuthType: toolpkg.AuthAPIKey,
		Fields: []toolpkg.CredentialField{
			{
				Key:          "email",
				Label:        "Garmin Connect email",
				Description:  "The email address for your Garmin Connect account.",
				Required:     true,
				EnvVarPrefix: "GARMIN_EMAIL",
			},
			{
				Key:          "password",
				Label:        "Garmin Connect password",
				Description:  "Used once to sign in; an OAuth token is stored and refreshed afterwards.",
				Required:     true,
				EnvVarPrefix: "GARMIN_PASSWORD",
			},
		},
		SupportsMultiple: false,
	}
}

// OnCredentialSetChange implements toolpkg.CredentialProvider. The Garmin tools are always
// registered and read credentials from the store at call time, so there is nothing to re-wire when
// a credential changes.
func (p *Package) OnCredentialSetChange(
	_ *mcp.Handler,
	_ toolpkg.RegistrationContext,
	_ []toolpkg.ResolvedCredentialSet,
) error {
	return nil
}
