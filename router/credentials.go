package router

import (
	"context"
	"log/slog"

	"tclaw/credential"
	"tclaw/mcp"
	"tclaw/tool/credentialtools"
	"tclaw/tool/toolpkg"
)

// registerCredentialSystem sets up the unified credential management for all
// tool packages that implement CredentialProvider. This is the generic
// replacement for the per-provider wiring in providers.go.
//
// It:
//  1. Seeds credentials from env vars into the credential store
//  2. Registers the credential management MCP tools (credential_add, etc.)
//  3. Loads existing credential sets and calls OnCredentialSetChange for each
//     CredentialProvider package so they can register their tools
//
// Returns the credential manager for use by other router functions.
func registerCredentialSystem(
	ctx context.Context,
	handler *mcp.Handler,
	registry *toolpkg.Registry,
	credMgr *credential.Manager,
	regCtx toolpkg.RegistrationContext,
) {
	userID := string(regCtx.UserID)

	// Seed pre-provisioned credentials from env vars.
	if err := registry.SeedCredentials(ctx, userID, credMgr); err != nil {
		slog.Error("failed to seed credentials from env", "user", userID, "err", err)
	}

	// Build the change callback that re-triggers OnCredentialSetChange for a package.
	onChange := func(packageName string) {
		notifyCredentialChange(ctx, handler, registry, credMgr, regCtx, packageName)
	}

	// Register the generic credential management tools.
	credentialtools.RegisterTools(handler, credentialtools.Deps{
		CredentialManager:  credMgr,
		Registry:           registry,
		Callback:           regCtx.Callback,
		OnCredentialChange: onChange,
	})

	// Call OnCredentialSetChange for all CredentialProvider packages that have
	// existing credential sets, so they can register their tools at startup.
	for _, cp := range registry.CredentialProviders() {
		notifyCredentialChange(ctx, handler, registry, credMgr, regCtx, cp.Name())
	}
}

// notifyCredentialChange loads credential sets for a package and calls
// OnCredentialSetChange so the package can update its tool registrations.
func notifyCredentialChange(
	ctx context.Context,
	handler *mcp.Handler,
	registry *toolpkg.Registry,
	credMgr *credential.Manager,
	regCtx toolpkg.RegistrationContext,
	packageName string,
) {
	// Find the CredentialProvider.
	var cp toolpkg.CredentialProvider
	for _, p := range registry.CredentialProviders() {
		if p.Name() == packageName {
			cp = p
			break
		}
	}
	if cp == nil {
		slog.Warn("credential change for unknown package", "package", packageName)
		return
	}

	spec := cp.CredentialSpec()

	sets, err := credMgr.ListByPackage(ctx, packageName)
	if err != nil {
		slog.Error("failed to list credential sets for package", "package", packageName, "err", err)
		return
	}

	// Resolve readiness for each set.
	var resolved []toolpkg.ResolvedCredentialSet
	for _, s := range sets {
		ready, readyErr := credMgr.IsReady(ctx, s.ID, spec.RequiredFieldKeys(), spec.NeedsOAuth())
		if readyErr != nil {
			slog.Warn("failed to check credential readiness", "set", s.ID, "err", readyErr)
		}
		resolved = append(resolved, toolpkg.ResolvedCredentialSet{
			CredentialSet: s,
			Ready:         ready,
		})
	}

	if err := cp.OnCredentialSetChange(handler, regCtx, resolved); err != nil {
		slog.Error("OnCredentialSetChange failed", "package", packageName, "err", err)
	}
}
