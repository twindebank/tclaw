package secretform

import (
	"context"
	"net/http"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// ExtraKeyBaseURL is the RegistrationContext.Extra key for the public base URL.
const ExtraKeyBaseURL = "secret_form_base_url"

// ExtraKeyRegisterHandler is the RegistrationContext.Extra key for the HTTP handler registration callback.
const ExtraKeyRegisterHandler = "secret_form_register_handler"

// Package implements toolpkg.Package for secret form tools.
type Package struct{}

func (p *Package) Name() string { return "secret_form" }
func (p *Package) Description() string {
	return "Collect sensitive information (API keys, tokens, passwords) via secure web forms. Values go directly to encrypted storage, never through chat."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupSecretForm }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__secret_form_*"}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec { return nil }

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Collect sensitive information via secure web forms."},
		Credentials: nil,
		Tools:       []string{"secret_form_request", "secret_form_wait"},
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, regCtx toolpkg.RegistrationContext) error {
	deps := Deps{
		SecretStore: regCtx.SecretStore,
	}

	if baseURL, ok := regCtx.Extra[ExtraKeyBaseURL].(string); ok {
		deps.BaseURL = baseURL
	}
	if regHandler, ok := regCtx.Extra[ExtraKeyRegisterHandler].(func(string, http.Handler)); ok {
		deps.RegisterHandler = regHandler
	}

	RegisterTools(handler, deps)
	return nil
}
