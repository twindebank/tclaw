// Package credentialtools provides MCP tools for managing credential sets.
//
// These tools replace the old connectiontools with a unified credential model.
// The agent uses credential_add to create credential sets (triggering either a
// secret form or an OAuth flow), credential_list to inspect status, and
// credential_remove to delete them.
package credentialtools

import (
	"context"

	"tclaw/internal/claudecli"
	"tclaw/internal/credential"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/oauth"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

const (
	ToolCredentialAdd      = "credential_add"
	ToolCredentialList     = "credential_list"
	ToolCredentialRemove   = "credential_remove"
	ToolCredentialAuthWait = "credential_auth_wait"
)

// Deps holds the dependencies for credential management tools.
type Deps struct {
	CredentialManager *credential.Manager
	Registry          *toolpkg.Registry
	Callback          *oauth.CallbackServer // nil if OAuth is not configured

	// OnCredentialChange is called after a credential set is added, removed,
	// or its fields are updated. The router uses this to trigger
	// OnCredentialSetChange on the affected tool package.
	OnCredentialChange func(packageName string)
}

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolCredentialAdd, ToolCredentialList, ToolCredentialRemove,
		ToolCredentialAuthWait,
	}
}

// RegisterTools adds the credential management tools to the MCP handler.
func RegisterTools(h *mcp.Handler, deps Deps) {
	h.Register(credentialListDef(), credentialListHandler(deps))
	h.Register(credentialAddDef(deps.Registry), credentialAddHandler(deps))
	h.Register(credentialRemoveDef(), credentialRemoveHandler(deps))
	if deps.Callback != nil {
		h.Register(credentialAuthWaitDef(), credentialAuthWaitHandler(deps))
	}
}

// Package implements toolpkg.Package for credential management tools.
type Package struct {
	CredentialManager *credential.Manager
	Registry          *toolpkg.Registry
}

func (p *Package) Name() string { return "credential" }
func (p *Package) Description() string {
	return "Manage credential sets for tool packages — add, list, and remove credentials for services like Google, TfL, Monzo, etc."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupConnections }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__credential_*"},
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec { return nil }

func (p *Package) Info(_ context.Context, _ secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Manage credentials for tool packages."},
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, regCtx toolpkg.RegistrationContext) error {
	RegisterTools(handler, Deps{
		CredentialManager:  p.CredentialManager,
		Registry:           p.Registry,
		Callback:           regCtx.Callback,
		OnCredentialChange: regCtx.OnCredentialChange,
	})

	return nil
}
