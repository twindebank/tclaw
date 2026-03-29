package tfl

import (
	"context"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// Package implements toolpkg.Package for Transport for London tools.
type Package struct{}

func (p *Package) Name() string { return "tfl" }
func (p *Package) Description() string {
	return "Transport for London: line status, journey planning, arrivals, stop search, disruptions, and road status. Works without an API key (rate-limited) or with one (higher limits)."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupPersonalServices }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__tfl_*"}
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
		Tools:       []string{"tfl_line_status", "tfl_journey", "tfl_arrivals", "tfl_stop_search", "tfl_disruptions", "tfl_road_status"},
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, ctx toolpkg.RegistrationContext) error {
	deps := Deps{SecretStore: ctx.SecretStore}
	RegisterTools(handler, deps)
	return nil
}
