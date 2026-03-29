package router

import (
	"context"
	"fmt"
	"log/slog"

	"tclaw/config"
	"tclaw/credential"
	"tclaw/mcp"
	"tclaw/tool/credentialtools"
	"tclaw/tool/toolpkg"
)

// seedConfigCredentials creates credential sets and writes secrets from the
// config file's credentials section. Runs every startup — idempotent because
// it overwrites existing field values with the config values.
func seedConfigCredentials(ctx context.Context, credMgr *credential.Manager, cfg config.CredentialsConfig) {
	for pkg, entries := range cfg {
		for _, entry := range entries {
			label := entry.Label
			if label == "" {
				label = "default"
			}

			setID := credential.NewCredentialSetID(pkg, label)

			// Ensure the credential set exists.
			existing, err := credMgr.Get(ctx, setID)
			if err != nil {
				slog.Error("config seed: failed to check credential set", "set", setID, "err", err)
				continue
			}
			if existing == nil {
				if _, err := credMgr.Add(ctx, pkg, label, entry.Channel); err != nil {
					slog.Error("config seed: failed to create credential set", "set", setID, "err", err)
					continue
				}
			}

			// Write each secret field.
			for key, val := range entry.Secrets {
				if val == "" {
					continue
				}
				if err := credMgr.SetField(ctx, setID, key, val); err != nil {
					slog.Error("config seed: failed to set field", "set", setID, "field", key, "err", err)
				}
			}
		}
	}
}

// buildConfigSecretsMap converts config credentials into a flat map for the
// legacy connection migration. Maps package name → field → value using the
// first entry's secrets for each package (legacy connections only had one
// set of client credentials per provider).
func buildConfigSecretsMap(cfg config.CredentialsConfig) map[string]map[string]string {
	result := make(map[string]map[string]string, len(cfg))
	for pkg, entries := range cfg {
		if len(entries) == 0 {
			continue
		}
		// Use the first entry's secrets for migration — legacy connections
		// didn't support multiple credential sets per provider.
		result[pkg] = entries[0].Secrets
	}
	return result
}

// registerCredentialSystem sets up the unified credential management for all
// tool packages that implement CredentialProvider.
//
// It:
//  1. Seeds credentials from env vars into the credential store
//  2. Registers the credential management MCP tools (credential_add, etc.)
//  3. Loads existing credential sets and calls OnCredentialSetChange for each
//     CredentialProvider package so they can register their tools
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
