package router

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// memorySecretStore is a simple in-memory secret.Store for testing.
type memorySecretStore struct {
	data map[string]string
}

func (m *memorySecretStore) Get(_ context.Context, key string) (string, error) {
	return m.data[key], nil
}

func (m *memorySecretStore) Set(_ context.Context, key, value string) error {
	m.data[key] = value
	return nil
}

func (m *memorySecretStore) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func TestSeedSecrets(t *testing.T) {
	t.Run("seeds env vars into store and unsets them", func(t *testing.T) {
		store := &memorySecretStore{data: make(map[string]string)}

		os.Setenv("TEST_SEED_A", "value-a")
		os.Setenv("TEST_SEED_B", "value-b")
		t.Cleanup(func() {
			os.Unsetenv("TEST_SEED_A")
			os.Unsetenv("TEST_SEED_B")
		})

		seeds := []SecretSeed{
			{EnvVarName: "TEST_SEED_A", StoreKey: "key_a"},
			{EnvVarName: "TEST_SEED_B", StoreKey: "key_b"},
		}

		err := SeedSecrets(context.Background(), store, seeds)
		require.NoError(t, err)

		// Values should be in the store.
		require.Equal(t, "value-a", store.data["key_a"])
		require.Equal(t, "value-b", store.data["key_b"])

		// Env vars should be unset.
		require.Empty(t, os.Getenv("TEST_SEED_A"))
		require.Empty(t, os.Getenv("TEST_SEED_B"))
	})

	t.Run("skips empty env vars", func(t *testing.T) {
		store := &memorySecretStore{data: make(map[string]string)}

		// Ensure env var is not set.
		os.Unsetenv("TEST_SEED_MISSING")

		seeds := []SecretSeed{
			{EnvVarName: "TEST_SEED_MISSING", StoreKey: "missing_key"},
		}

		err := SeedSecrets(context.Background(), store, seeds)
		require.NoError(t, err)

		// Store should not have the key.
		require.Empty(t, store.data["missing_key"])
	})

	t.Run("returns error on store failure", func(t *testing.T) {
		store := &failingSecretStore{}

		os.Setenv("TEST_SEED_FAIL", "some-value")
		t.Cleanup(func() { os.Unsetenv("TEST_SEED_FAIL") })

		seeds := []SecretSeed{
			{EnvVarName: "TEST_SEED_FAIL", StoreKey: "fail_key"},
		}

		err := SeedSecrets(context.Background(), store, seeds)
		require.Error(t, err)
		require.Contains(t, err.Error(), "seed fail_key from TEST_SEED_FAIL")
	})

	t.Run("stops at first store error", func(t *testing.T) {
		store := &failingSecretStore{}

		os.Setenv("TEST_SEED_X", "x-val")
		os.Setenv("TEST_SEED_Y", "y-val")
		t.Cleanup(func() {
			os.Unsetenv("TEST_SEED_X")
			os.Unsetenv("TEST_SEED_Y")
		})

		seeds := []SecretSeed{
			{EnvVarName: "TEST_SEED_X", StoreKey: "x_key"},
			{EnvVarName: "TEST_SEED_Y", StoreKey: "y_key"},
		}

		err := SeedSecrets(context.Background(), store, seeds)
		require.Error(t, err)
		// First seed fails, second is never attempted.
		require.Contains(t, err.Error(), "x_key")
	})
}

func TestSecretSeedEnvVarName(t *testing.T) {
	t.Run("simple user ID", func(t *testing.T) {
		require.Equal(t, "GITHUB_TOKEN_THEO", SecretSeedEnvVarName("GITHUB_TOKEN", "theo"))
	})

	t.Run("user ID with hyphens", func(t *testing.T) {
		require.Equal(t, "FLY_TOKEN_MY_USER", SecretSeedEnvVarName("FLY_TOKEN", "my-user"))
	})

	t.Run("user ID with mixed case", func(t *testing.T) {
		require.Equal(t, "TFL_API_KEY_ADMIN", SecretSeedEnvVarName("TFL_API_KEY", "Admin"))
	})
}

// --- helpers ---

type failingSecretStore struct{}

func (f *failingSecretStore) Get(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (f *failingSecretStore) Set(_ context.Context, _ string, _ string) error {
	return context.DeadlineExceeded
}

func (f *failingSecretStore) Delete(_ context.Context, _ string) error {
	return nil
}
