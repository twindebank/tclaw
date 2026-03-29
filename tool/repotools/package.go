package repotools

import (
	"context"
	"fmt"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/repo"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// ExtraKeyRepoStore is the RegistrationContext.Extra key for *repo.Store.
const ExtraKeyRepoStore = "repo_store"

// Package implements toolpkg.Package for repository monitoring tools.
type Package struct{}

func (p *Package) Name() string { return "repo" }
func (p *Package) Description() string {
	return "Monitor external git repositories: add, sync, view logs, list, remove. Read-only — for tracking changes, not making them."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupRepoMonitoring }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__repo_*"}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec {
	return []toolpkg.SecretSpec{
		{
			StoreKey:     "github_token",
			EnvVarPrefix: "GITHUB_TOKEN",
			Required:     false,
			Label:        "GitHub Token",
			Description:  "Personal access token for cloning private repositories.",
		},
	}
}

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Monitor external git repositories."},
		Credentials: toolpkg.CheckCredentialStatus(ctx, secretStore, p.RequiredSecrets()),
		Tools:       []string{"repo_add", "repo_sync", "repo_log", "repo_list", "repo_remove"},
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, ctx toolpkg.RegistrationContext) error {
	repoStore, ok := ctx.Extra[ExtraKeyRepoStore].(*repo.Store)
	if !ok || repoStore == nil {
		return fmt.Errorf("repotools: missing %s in RegistrationContext.Extra", ExtraKeyRepoStore)
	}

	RegisterTools(handler, Deps{
		Store:       repoStore,
		SecretStore: ctx.SecretStore,
		UserDir:     ctx.UserDir,
	})
	return nil
}
