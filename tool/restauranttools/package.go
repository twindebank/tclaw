package restauranttools

import (
	"context"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// Package implements toolpkg.Package for restaurant booking tools.
type Package struct{}

func (p *Package) Name() string { return "restaurant" }
func (p *Package) Description() string {
	return "Restaurant search and booking via Resy. Search for restaurants, check availability, book tables, cancel bookings, and list existing reservations."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupPersonalServices }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__restaurant_*"}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec {
	return []toolpkg.SecretSpec{
		{
			StoreKey:     ResyAPIKeyStoreKey,
			EnvVarPrefix: "RESY_API_KEY",
			Required:     true,
			Label:        "Resy API Key",
			Description:  "Authorization header value from browser dev tools (after 'ResyAPI api_key=').",
		},
		{
			StoreKey:     ResyAuthTokenStoreKey,
			EnvVarPrefix: "RESY_AUTH_TOKEN",
			Required:     true,
			Label:        "Resy Auth Token",
			Description:  "X-Resy-Auth-Token header value from browser dev tools.",
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
		OnCredentialsStored: func() {
			RegisterTools(handler, Deps{SecretStore: regCtx.SecretStore})
		},
	}

	// Always register info/setup tools.
	RegisterInfoTools(handler, deps)

	// Register operational tools if credentials already configured.
	if hasResyCredentials(context.Background(), regCtx.SecretStore) {
		RegisterTools(handler, deps)
	}

	return nil
}

func hasResyCredentials(ctx context.Context, store secret.Store) bool {
	apiKey, _ := store.Get(ctx, ResyAPIKeyStoreKey)
	authToken, _ := store.Get(ctx, ResyAuthTokenStoreKey)
	return apiKey != "" && authToken != ""
}

// CredentialSpec implements toolpkg.CredentialProvider.
func (p *Package) CredentialSpec() toolpkg.CredentialSpec {
	return toolpkg.CredentialSpec{
		AuthType: toolpkg.AuthAPIKey,
		Fields: []toolpkg.CredentialField{
			{Key: "api_key", Label: "Resy API Key", Description: "Authorization header value from browser dev tools (after 'ResyAPI api_key=').", Required: true, EnvVarPrefix: "RESY_API_KEY"},
			{Key: "auth_token", Label: "Resy Auth Token", Description: "X-Resy-Auth-Token header value from browser dev tools.", Required: true, EnvVarPrefix: "RESY_AUTH_TOKEN"},
		},
	}
}

// OnCredentialSetChange implements toolpkg.CredentialProvider. Currently a
// no-op — restaurant tools are still registered via the old Register path.
func (p *Package) OnCredentialSetChange(handler *mcp.Handler, ctx toolpkg.RegistrationContext, sets []toolpkg.ResolvedCredentialSet) error {
	return nil
}
