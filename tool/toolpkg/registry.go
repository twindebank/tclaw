package toolpkg

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"tclaw/claudecli"
	"tclaw/credential"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/toolgroup"
)

// Registry collects tool packages and provides bulk operations for
// registration, secret seeding, and introspection.
type Registry struct {
	packages []Package
}

// NewRegistry creates a registry from the given packages. Panics on duplicate
// package names to catch wiring bugs at startup.
func NewRegistry(packages ...Package) *Registry {
	seen := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		if seen[pkg.Name()] {
			panic(fmt.Sprintf("toolpkg: duplicate package name %q", pkg.Name()))
		}
		seen[pkg.Name()] = true
	}
	return &Registry{packages: packages}
}

// RegisterAll registers every package's tools on the handler, plus an
// auto-generated <name>_info tool for each package.
func (r *Registry) RegisterAll(handler *mcp.Handler, regCtx RegistrationContext) error {
	for _, pkg := range r.packages {
		// Register the package's own tools.
		if err := pkg.Register(handler, regCtx); err != nil {
			return fmt.Errorf("register %s: %w", pkg.Name(), err)
		}

		// Auto-register the standard info tool.
		handler.Register(InfoToolDef(pkg), InfoToolHandler(pkg, regCtx.SecretStore))

		slog.Debug("registered tool package", "name", pkg.Name(), "group", pkg.Group())
	}
	return nil
}

// SeedAllSecrets reads pre-provisioned secrets from environment variables
// and stores them in the encrypted secret store. Each package declares its
// secrets via RequiredSecrets(); this method iterates all of them and seeds
// any that have a corresponding env var set. The env var is unset after seeding.
func (r *Registry) SeedAllSecrets(ctx context.Context, userID string, store secret.Store) error {
	for _, pkg := range r.packages {
		for _, spec := range pkg.RequiredSecrets() {
			if spec.EnvVarPrefix == "" {
				continue
			}
			envVar := spec.EnvVarPrefix + "_" + sanitizeEnvSuffix(userID)
			val := os.Getenv(envVar)
			if val == "" {
				continue
			}
			if err := store.Set(ctx, spec.StoreKey, val); err != nil {
				return fmt.Errorf("seed %s from %s: %w", spec.StoreKey, envVar, err)
			}
			os.Unsetenv(envVar)
			slog.Debug("seeded and scrubbed secret from env", "env_var", envVar, "store_key", spec.StoreKey)
		}
	}
	return nil
}

// AllInfo returns PackageInfo for every registered package.
func (r *Registry) AllInfo(ctx context.Context, store secret.Store) []PackageInfo {
	infos := make([]PackageInfo, 0, len(r.packages))
	for _, pkg := range r.packages {
		info, err := pkg.Info(ctx, store)
		if err != nil {
			slog.Warn("failed to get info for package", "name", pkg.Name(), "err", err)
			continue
		}
		infos = append(infos, *info)
	}
	return infos
}

// BuildGroupTools builds a toolgroup -> tools map from all registered packages.
// Each package declares its group and tool patterns; this method collects them.
func (r *Registry) BuildGroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	result := make(map[toolgroup.ToolGroup][]claudecli.Tool)
	for _, pkg := range r.packages {
		group := pkg.Group()
		if group == "" {
			continue
		}
		result[group] = append(result[group], pkg.ToolPatterns()...)
	}
	return result
}

// Packages returns all registered packages.
func (r *Registry) Packages() []Package {
	return r.packages
}

// SeedCredentials reads pre-provisioned secrets from environment variables and
// stores them in credential sets for packages that implement CredentialProvider.
// For each CredentialField with an EnvVarPrefix, it checks for an env var of
// the form PREFIX_USERID and seeds the value into the credential manager.
func (r *Registry) SeedCredentials(ctx context.Context, userID string, credMgr *credential.Manager) error {
	for _, pkg := range r.packages {
		cp, ok := pkg.(CredentialProvider)
		if !ok {
			continue
		}

		spec := cp.CredentialSpec()
		for _, field := range spec.Fields {
			if field.EnvVarPrefix == "" {
				continue
			}
			envVar := field.EnvVarPrefix + "_" + sanitizeEnvSuffix(userID)
			val := os.Getenv(envVar)
			if val == "" {
				continue
			}

			// Ensure a default credential set exists for this package.
			id := credential.NewCredentialSetID(pkg.Name(), "default")
			existing, err := credMgr.Get(ctx, id)
			if err != nil {
				return fmt.Errorf("check credential set %s: %w", id, err)
			}
			if existing == nil {
				if _, err := credMgr.Add(ctx, pkg.Name(), "default", ""); err != nil {
					return fmt.Errorf("create default credential set for %s: %w", pkg.Name(), err)
				}
			}

			if err := credMgr.SetField(ctx, id, field.Key, val); err != nil {
				return fmt.Errorf("seed %s/%s from %s: %w", pkg.Name(), field.Key, envVar, err)
			}
			os.Unsetenv(envVar)
			slog.Debug("seeded credential field from env", "env_var", envVar, "package", pkg.Name(), "field", field.Key)
		}
	}
	return nil
}

// CredentialProviders returns all packages that implement CredentialProvider.
func (r *Registry) CredentialProviders() []CredentialProvider {
	var providers []CredentialProvider
	for _, pkg := range r.packages {
		if cp, ok := pkg.(CredentialProvider); ok {
			providers = append(providers, cp)
		}
	}
	return providers
}

// sanitizeEnvSuffix uppercases a user ID and replaces non-alphanumeric chars
// with underscores, producing a safe environment variable suffix.
func sanitizeEnvSuffix(userID string) string {
	return strings.ToUpper(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, userID))
}
