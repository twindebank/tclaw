package devtools

import (
	"context"
	"fmt"

	"tclaw/claudecli"
	"tclaw/dev"
	"tclaw/libraries/logbuffer"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// ExtraKeyDevStore is the RegistrationContext.Extra key for *dev.Store.
const ExtraKeyDevStore = "dev_store"

// ExtraKeyLogBuffer is the RegistrationContext.Extra key for *logbuffer.Buffer.
const ExtraKeyLogBuffer = "log_buffer"

// Package implements toolpkg.Package for dev workflow tools.
type Package struct{}

func (p *Package) Name() string { return "dev" }
func (p *Package) Description() string {
	return "Dev workflow: start/end/cancel dev sessions, view status and logs, create PRs, deploy to production, inspect disk usage, and manage tclaw config."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupDevWorkflow }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__dev_*", "mcp__tclaw__config_*"}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec {
	return []toolpkg.SecretSpec{
		{
			StoreKey:     githubTokenKey,
			EnvVarPrefix: "GITHUB_TOKEN",
			Required:     false,
			Label:        "GitHub Token",
			Description:  "Personal access token for git push and PR creation via gh.",
		},
		{
			StoreKey:     flyTokenKey,
			EnvVarPrefix: "FLY_TOKEN",
			Required:     false,
			Label:        "Fly.io API Token",
			Description:  "API token for deploying to Fly.io via fly deploy.",
		},
	}
}

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Dev workflow: start/end/cancel dev sessions, view status and logs, deploy to production."},
		Credentials: toolpkg.CheckCredentialStatus(ctx, secretStore, p.RequiredSecrets()),
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, ctx toolpkg.RegistrationContext) error {
	devStore, ok := ctx.Extra[ExtraKeyDevStore].(*dev.Store)
	if !ok || devStore == nil {
		return fmt.Errorf("devtools: missing %s in RegistrationContext.Extra", ExtraKeyDevStore)
	}

	var logBuffer *logbuffer.Buffer
	if lb, ok := ctx.Extra[ExtraKeyLogBuffer]; ok {
		logBuffer, _ = lb.(*logbuffer.Buffer)
	}

	RegisterTools(handler, Deps{
		Store:       devStore,
		SecretStore: ctx.SecretStore,
		UserDir:     ctx.UserDir,
		UserID:      ctx.UserID,
		LogBuffer:   logBuffer,
		ConfigPath:  ctx.ConfigPath,
	})
	return nil
}

// CredentialSpec implements toolpkg.CredentialProvider.
func (p *Package) CredentialSpec() toolpkg.CredentialSpec {
	return toolpkg.CredentialSpec{
		AuthType: toolpkg.AuthAPIKey,
		Fields: []toolpkg.CredentialField{
			{Key: "github_token", Label: "GitHub Token", Description: "Personal access token for git push and PR creation via gh.", Required: false, EnvVarPrefix: "GITHUB_TOKEN"},
			{Key: "fly_api_token", Label: "Fly.io API Token", Description: "API token for deploying to Fly.io via fly deploy.", Required: false, EnvVarPrefix: "FLY_TOKEN"},
		},
	}
}

// OnCredentialSetChange implements toolpkg.CredentialProvider. Currently a
// no-op — dev tools are always registered and read secrets at call time.
func (p *Package) OnCredentialSetChange(handler *mcp.Handler, ctx toolpkg.RegistrationContext, sets []toolpkg.ResolvedCredentialSet) error {
	return nil
}
