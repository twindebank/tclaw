package router

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"tclaw/internal/config"
	"tclaw/internal/credential"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/toolpkg"
)

// seedCredentialSlots creates a credential set for every slot declared in config
// and writes any field values it supplies. Runs every startup — idempotent
// because it overwrites existing field values with the config values.
//
// A slot with no fields is still created. That is the point of declaring one:
// the set exists and can be referenced and filled later (secret form, OAuth
// flow) without a config edit or a deploy.
func seedCredentialSlots(ctx context.Context, credMgr *credential.Manager, slots []config.CredentialSlot) error {
	for _, slot := range slots {
		label := slot.Label
		if label == "" {
			label = "default"
		}

		setID := credential.NewCredentialSetID(slot.Type, label)

		existing, err := credMgr.Get(ctx, setID)
		if err != nil {
			return fmt.Errorf("check credential slot %s: %w", setID, err)
		}
		if existing == nil {
			if _, err := credMgr.Add(ctx, credential.AddParams{
				Package:     slot.Type,
				Label:       label,
				Channel:     slot.Channel,
				Description: slot.Description,
			}); err != nil {
				return fmt.Errorf("create credential slot %s: %w", setID, err)
			}
		}

		for key, val := range slot.Fields {
			// An empty value leaves whatever is already stored alone, so a
			// boot secret that has gone missing doesn't wipe a working
			// credential the user filled in by hand.
			if val == "" {
				continue
			}
			if err := credMgr.SetField(ctx, setID, key, val); err != nil {
				return fmt.Errorf("set field %s on %s: %w", key, setID, err)
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

	// Notify credential providers in parallel so network-bound operations
	// (OAuth token verification, account detail loading) don't serialize.
	var wg sync.WaitGroup
	for _, cp := range registry.CredentialProviders() {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			notifyCredentialChange(ctx, handler, registry, credMgr, regCtx, name)
		}(cp.Name())
	}
	wg.Wait()
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
