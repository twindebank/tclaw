package router

import (
	"context"
	"fmt"
	"log/slog"

	"tclaw/config"
	"tclaw/credential"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
)

// seedConfigCredentials creates credential sets and writes secrets from the
// config file's credentials section. Runs every startup — idempotent because
// it overwrites existing field values with the config values.
func seedConfigCredentials(ctx context.Context, credMgr *credential.Manager, cfg config.CredentialsConfig) error {
	for pkg, entries := range cfg {
		for _, entry := range entries {
			label := entry.Label
			if label == "" {
				label = "default"
			}

			setID := credential.NewCredentialSetID(pkg, label)

			existing, err := credMgr.Get(ctx, setID)
			if err != nil {
				return fmt.Errorf("check credential set %s: %w", setID, err)
			}
			if existing == nil {
				if _, err := credMgr.Add(ctx, pkg, label, entry.Channel); err != nil {
					return fmt.Errorf("create credential set %s: %w", setID, err)
				}
			}

			for key, val := range entry.Secrets {
				if val == "" {
					continue
				}
				if err := credMgr.SetField(ctx, setID, key, val); err != nil {
					return fmt.Errorf("set field %s on %s: %w", key, setID, err)
				}
			}
		}
	}
	return nil
}

// registerCredentialSystem sets up the unified credential management for all
// tool packages in the registry.
//
// It:
//  1. Registers all packages via RegisterAll (info tools + each package's Register method)
//  2. Seeds credentials from env vars into the credential store
//  3. Wires up the OnCredentialChange callback for dynamic tool registration
//  4. Calls OnCredentialSetChange for each CredentialProvider so they register
//     their tools based on existing credentials at startup
func registerCredentialSystem(
	ctx context.Context,
	handler *mcp.Handler,
	registry *toolpkg.Registry,
	credMgr *credential.Manager,
	regCtx toolpkg.RegistrationContext,
	userID string,
) {
	// Wire up the change callback so credential_add/remove re-triggers
	// OnCredentialSetChange for the affected package.
	regCtx.OnCredentialChange = func(packageName string) {
		notifyCredentialChange(ctx, handler, registry, credMgr, regCtx, packageName)
	}

	// Register all packages — info tools and each package's Register method.
	// For credentialtools this registers credential_add/list/remove. For
	// Google/Monzo the Register method is a no-op (tools registered via
	// OnCredentialSetChange below).
	if err := registry.RegisterAll(handler, regCtx); err != nil {
		slog.Error("failed to register tool packages", "user", userID, "err", err)
		return
	}

	// Seed pre-provisioned credentials from env vars.
	if err := registry.SeedCredentials(ctx, userID, credMgr); err != nil {
		slog.Error("failed to seed credentials from env", "user", userID, "err", err)
	}

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

// credentialFieldStoreKey builds the secret store key for a credential field.
// Exported so other router code can compute keys consistently.
func credentialFieldStoreKey(id credential.CredentialSetID, field string) string {
	return fmt.Sprintf("cred/%s/%s", id, field)
}
