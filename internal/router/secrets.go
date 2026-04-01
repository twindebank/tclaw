package router

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"tclaw/internal/libraries/secret"
)

// SecretSeed maps an environment variable to a secret store key. Used to seed
// pre-provisioned secrets (e.g. Fly secrets) into the encrypted per-user store
// at boot time.
type SecretSeed struct {
	// EnvVarName is the full environment variable name (e.g. "GITHUB_TOKEN_THEO").
	EnvVarName string

	// StoreKey is the key in the secret store (e.g. "github_token").
	StoreKey string
}

// SeedSecrets reads each environment variable, stores its value in the secret
// store, and unsets the env var. Returns the first error encountered during
// storage (after logging all errors). Env vars that are empty or unset are
// silently skipped.
func SeedSecrets(ctx context.Context, store secret.Store, seeds []SecretSeed) error {
	for _, seed := range seeds {
		val := os.Getenv(seed.EnvVarName)
		if val == "" {
			continue
		}
		if err := store.Set(ctx, seed.StoreKey, val); err != nil {
			return fmt.Errorf("seed %s from %s: %w", seed.StoreKey, seed.EnvVarName, err)
		}
		os.Unsetenv(seed.EnvVarName)
		slog.Debug("seeded and scrubbed secret from env", "env_var", seed.EnvVarName, "store_key", seed.StoreKey)
	}
	return nil
}

// SecretSeedEnvVarName builds a per-user env var name from a prefix and user ID.
// The user ID is uppercased with non-alphanumeric chars replaced by underscores.
//
//	SecretSeedEnvVarName("GITHUB_TOKEN", "theo") => "GITHUB_TOKEN_THEO"
//	SecretSeedEnvVarName("TFL_API_KEY", "my-user") => "TFL_API_KEY_MY_USER"
func SecretSeedEnvVarName(prefix string, userID string) string {
	return prefix + "_" + sanitizeEnvSuffix(userID)
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
