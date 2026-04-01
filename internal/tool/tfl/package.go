package tfl

import (
	"context"

	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

// Package implements toolpkg.Package and toolpkg.CredentialProvider for
// Transport for London tools.
type Package struct {
	SecretStore secret.Store
}

func (p *Package) Name() string { return "tfl" }
func (p *Package) Description() string {
	return "Transport for London: line status, journey planning, arrivals, stop search, disruptions, and road status. Works without an API key (rate-limited) or with one (higher limits)."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupPersonalServices }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__tfl_*"},
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec {
	return []toolpkg.SecretSpec{
		{
			StoreKey:     APIKeyStoreKey,
			EnvVarPrefix: "TFL_API_KEY",
			Required:     false,
			Label:        "TfL API Key",
			Description:  "Register free at https://api-portal.tfl.gov.uk/products to get higher rate limits.",
		},
	}
}

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Personal service integrations: TfL transport, restaurant reservations, banking, Monzo."},
		Credentials: toolpkg.CheckCredentialStatus(ctx, secretStore, p.RequiredSecrets()),
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, ctx toolpkg.RegistrationContext) error {
	deps := Deps{SecretStore: p.SecretStore}
	RegisterTools(handler, deps)
	return nil
}

// CredentialSpec implements toolpkg.CredentialProvider. TfL has a single
// optional API key field.
func (p *Package) CredentialSpec() toolpkg.CredentialSpec {
	return toolpkg.CredentialSpec{
		AuthType: toolpkg.AuthAPIKey,
		Fields: []toolpkg.CredentialField{
			{
				Key:          "api_key",
				Label:        "TfL API Key",
				Description:  "Register free at https://api-portal.tfl.gov.uk/products to get higher rate limits.",
				Required:     false,
				EnvVarPrefix: "TFL_API_KEY",
			},
		},
		SupportsMultiple: false,
	}
}

// OnCredentialSetChange implements toolpkg.CredentialProvider. TfL tools are
// always registered (they work without an API key), so this is a no-op.
// The tools read the API key from the secret store at call time.
func (p *Package) OnCredentialSetChange(handler *mcp.Handler, ctx toolpkg.RegistrationContext, sets []toolpkg.ResolvedCredentialSet) error {
	// TfL tools are always registered via Register() — they work in degraded
	// mode without an API key. Nothing to do here.
	return nil
}
