package secretform

import (
	"context"
	"net/http"

	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

// Package implements toolpkg.Package for secret form tools.
type Package struct {
	SecretStore     secret.Store
	BaseURL         string
	RegisterHandler func(string, http.Handler)
}

func (p *Package) Name() string { return "secret_form" }
func (p *Package) Description() string {
	return "Collect sensitive information (API keys, tokens, passwords) via secure web forms. Values go directly to encrypted storage, never through chat."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupSecretForm }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__secret_form_*"},
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec { return nil }

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Collect sensitive information via secure web forms."},
		Credentials: nil,
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, regCtx toolpkg.RegistrationContext) error {
	deps := Deps{
		SecretStore:     p.SecretStore,
		BaseURL:         p.BaseURL,
		RegisterHandler: p.RegisterHandler,
	}

	RegisterTools(handler, deps)
	return nil
}
