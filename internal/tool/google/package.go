package google

import (
	"context"
	"fmt"
	"log/slog"

	"tclaw/internal/claudecli"
	"tclaw/internal/credential"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/notification"
	"tclaw/internal/tool/providerutil"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

// Package implements toolpkg.Package and toolpkg.CredentialProvider for
// Google Workspace tools.
//
// Currently, tools are still registered dynamically per-connection by the
// router's OnProviderConnect callback. The CredentialProvider interface
// declares the OAuth config so it's owned by this package rather than
// provider/google.go.
type Package struct {
	// NotificationManager is used to register/unregister the Gmail notifier
	// when credentials become ready or are revoked.
	NotificationManager *notification.Manager
}

func (p *Package) Name() string { return "google" }
func (p *Package) Description() string {
	return "Google Workspace: Gmail (list, read, send), Calendar (list, create), and workspace data access. Requires an OAuth connection via connection_add."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupGSuiteWrite }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		// GSuiteWrite includes all Google tools (superset of GSuiteRead).
		toolgroup.GroupGSuiteWrite: {"mcp__tclaw__google_*"},
		// GSuiteRead includes only the read-only subset.
		toolgroup.GroupGSuiteRead: {
			"mcp__tclaw__google_gmail_list",
			"mcp__tclaw__google_gmail_read",
			"mcp__tclaw__google_workspace",
			"mcp__tclaw__google_workspace_schema",
		},
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec {
	// Google uses OAuth connections, not direct secrets.
	return nil
}

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Google Workspace full access."},
		Credentials: nil,
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, regCtx toolpkg.RegistrationContext) error {
	// No-op: Google tools are registered dynamically per-connection by the
	// router's OnProviderConnect callback. This package provides metadata
	// and info tool only.
	return nil
}

// CredentialSpec implements toolpkg.CredentialProvider. Google requires OAuth2
// with client_id/client_secret from the tclaw config.
func (p *Package) CredentialSpec() toolpkg.CredentialSpec {
	return toolpkg.CredentialSpec{
		AuthType: toolpkg.AuthOAuth2,
		Fields: []toolpkg.CredentialField{
			{Key: "client_id", Label: "Google OAuth Client ID", Required: true},
			{Key: "client_secret", Label: "Google OAuth Client Secret", Required: true},
		},
		OAuth: &toolpkg.OAuthSpec{
			AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
			Scopes: []string{
				"https://www.googleapis.com/auth/gmail.modify",
				"https://www.googleapis.com/auth/drive",
				"https://www.googleapis.com/auth/calendar",
				"https://www.googleapis.com/auth/documents",
				"https://www.googleapis.com/auth/spreadsheets",
				"https://www.googleapis.com/auth/presentations",
				"https://www.googleapis.com/auth/tasks",
			},
			ExtraParams: map[string]string{
				"access_type": "offline",
				"prompt":      "consent",
			},
			Services: []string{"Gmail", "Google Drive", "Google Calendar", "Google Docs", "Google Sheets", "Google Slides", "Google Tasks"},
		},
		SupportsMultiple: true,
		ConfigKey:        "providers.google",
	}
}

// OnCredentialSetChange implements toolpkg.CredentialProvider. Registers or
// unregisters Google Workspace tools based on which credential sets have OAuth
// tokens ready. Also registers/unregisters the Gmail notifier with the
// notification manager so the agent can discover and subscribe to new_email.
func (p *Package) OnCredentialSetChange(handler *mcp.Handler, regCtx toolpkg.RegistrationContext, sets []toolpkg.ResolvedCredentialSet) error {
	credMgr := regCtx.CredentialManager
	if credMgr == nil {
		return fmt.Errorf("google: missing credential manager in RegistrationContext")
	}

	spec := p.CredentialSpec()
	resolved := toResolvedSets(sets)
	depsMap, err := providerutil.BuildDepsMap(context.Background(), credMgr, toOAuthSpec(spec.OAuth), resolved)
	if err != nil {
		return fmt.Errorf("google: build deps: %w", err)
	}

	if len(depsMap) == 0 {
		UnregisterTools(handler)
		if p.NotificationManager != nil {
			p.NotificationManager.UnregisterNotifier(p.Name())
		}
		return nil
	}

	RegisterTools(handler, depsMap)

	// Register the Gmail notifier so the agent can discover and subscribe
	// to new_email notifications. The depsMap closure rebuilds credentials
	// on each poll so it stays fresh after token refreshes.
	if p.NotificationManager != nil && regCtx.StateStore != nil {
		depsFunc := func() map[credential.CredentialSetID]Deps {
			freshSpec := p.CredentialSpec()
			freshResolved := toResolvedSets(sets)
			freshDeps, buildErr := providerutil.BuildDepsMap(context.Background(), credMgr, toOAuthSpec(freshSpec.OAuth), freshResolved)
			if buildErr != nil {
				slog.Error("google notifier: failed to rebuild deps", "error", buildErr)
				return nil
			}
			return freshDeps
		}
		notif := NewNotifier(depsFunc, regCtx.StateStore)
		p.NotificationManager.RegisterNotifier(p.Name(), notif)
	}

	return nil
}

// NewNotifier creates a notification.Notifier for Gmail notifications.
// The depsMap function is called on each poll to get fresh credentials.
// The state store persists the Gmail history cursor across restarts.
func NewNotifier(depsMap func() map[credential.CredentialSetID]Deps, state store.Store) notification.Notifier {
	return newNotifier(depsMap, state)
}

// toResolvedSets converts toolpkg.ResolvedCredentialSet to providerutil.ResolvedSet.
func toResolvedSets(sets []toolpkg.ResolvedCredentialSet) []providerutil.ResolvedSet {
	result := make([]providerutil.ResolvedSet, len(sets))
	for i, s := range sets {
		result[i] = providerutil.ResolvedSet{ID: s.ID, Ready: s.Ready}
	}
	return result
}

// toOAuthSpec converts a toolpkg.OAuthSpec to providerutil.OAuthSpec.
func toOAuthSpec(spec *toolpkg.OAuthSpec) providerutil.OAuthSpec {
	return providerutil.OAuthSpec{
		AuthURL:     spec.AuthURL,
		TokenURL:    spec.TokenURL,
		Scopes:      spec.Scopes,
		ExtraParams: spec.ExtraParams,
	}
}
