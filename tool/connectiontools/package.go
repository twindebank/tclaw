package connectiontools

import (
	"context"
	"fmt"

	"tclaw/claudecli"
	"tclaw/connection"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/provider"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// ExtraKeyConnManager is the RegistrationContext.Extra key for *connection.Manager.
const ExtraKeyConnManager = "connection_manager"

// ExtraKeyProviderRegistry is the RegistrationContext.Extra key for *provider.Registry.
const ExtraKeyProviderRegistry = "provider_registry"

// ExtraKeyMCPHandler is the RegistrationContext.Extra key for *mcp.Handler.
const ExtraKeyMCPHandler = "mcp_handler"

// ExtraKeyOnProviderConnect is the RegistrationContext.Extra key for the connect callback.
const ExtraKeyOnProviderConnect = "on_provider_connect"

// ExtraKeyOnProviderDisconnect is the RegistrationContext.Extra key for the disconnect callback.
const ExtraKeyOnProviderDisconnect = "on_provider_disconnect"

// Package implements toolpkg.Package for OAuth connection management tools.
type Package struct{}

func (p *Package) Name() string { return "connection" }
func (p *Package) Description() string {
	return "Manage OAuth connections to external services (Google, Monzo) and discover available providers."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupConnections }

func (p *Package) ToolPatterns() []claudecli.Tool {
	return []claudecli.Tool{"mcp__tclaw__connection_*"}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec { return nil }

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Manage OAuth connections to external services."},
		Credentials: nil,
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, regCtx toolpkg.RegistrationContext) error {
	connMgr, ok := regCtx.Extra[ExtraKeyConnManager].(*connection.Manager)
	if !ok || connMgr == nil {
		return fmt.Errorf("connectiontools: missing %s in Extra", ExtraKeyConnManager)
	}
	provRegistry, ok := regCtx.Extra[ExtraKeyProviderRegistry].(*provider.Registry)
	if !ok || provRegistry == nil {
		return fmt.Errorf("connectiontools: missing %s in Extra", ExtraKeyProviderRegistry)
	}

	var onConnect func(connection.ConnectionID, *connection.Manager, *provider.Provider)
	if fn, ok := regCtx.Extra[ExtraKeyOnProviderConnect].(func(connection.ConnectionID, *connection.Manager, *provider.Provider)); ok {
		onConnect = fn
	}
	var onDisconnect func(connection.ConnectionID)
	if fn, ok := regCtx.Extra[ExtraKeyOnProviderDisconnect].(func(connection.ConnectionID)); ok {
		onDisconnect = fn
	}

	RegisterTools(handler, Deps{
		Manager:              connMgr,
		Registry:             provRegistry,
		Callback:             regCtx.Callback,
		Handler:              handler,
		OnProviderConnect:    onConnect,
		OnProviderDisconnect: onDisconnect,
	})

	// Register auth_wait tool if OAuth is configured.
	if regCtx.Callback != nil {
		RegisterAuthWaitTool(handler, connMgr)
	}

	return nil
}
