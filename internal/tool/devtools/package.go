package devtools

import (
	"context"

	"tclaw/internal/claudecli"
	"tclaw/internal/dev"
	"tclaw/internal/libraries/logbuffer"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
	"tclaw/internal/user"
)

// Package implements toolpkg.Package for dev workflow tools.
type Package struct {
	Store       *dev.Store
	LogBuffer   *logbuffer.Buffer
	SecretStore secret.Store
	UserDir     string
	UserID      user.ID
	ConfigPath  string

	// ActiveChannel returns the channel name currently being processed. May be
	// nil in tests. Threaded through to dev_start so sessions record which
	// channel spawned them for ephemeral cleanup.
	ActiveChannel func() string
}

func (p *Package) Name() string { return "dev" }
func (p *Package) Description() string {
	return "Dev workflow: start/end/cancel dev sessions, view status and logs, create PRs, deploy to production, inspect disk usage, and manage tclaw config."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupDevWorkflow }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__dev_*", "mcp__tclaw__config_*"},
	}
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
	RegisterTools(handler, Deps{
		Store:         p.Store,
		SecretStore:   p.SecretStore,
		UserDir:       p.UserDir,
		UserID:        p.UserID,
		LogBuffer:     p.LogBuffer,
		ConfigPath:    p.ConfigPath,
		ActiveChannel: p.ActiveChannel,
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
