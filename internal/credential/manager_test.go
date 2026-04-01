package credential_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/credential"
	"tclaw/internal/libraries/store"
)

func TestManager_AddAndList(t *testing.T) {
	t.Run("add and list by package", func(t *testing.T) {
		mgr := setup(t)
		ctx := context.Background()

		set, err := mgr.Add(ctx, "google", "work", "admin")
		require.NoError(t, err)
		require.Equal(t, credential.CredentialSetID("google/work"), set.ID)
		require.Equal(t, "google", set.Package)
		require.Equal(t, "work", set.Label)
		require.Equal(t, "admin", set.Channel)

		sets, err := mgr.ListByPackage(ctx, "google")
		require.NoError(t, err)
		require.Len(t, sets, 1)
		require.Equal(t, set.ID, sets[0].ID)
	})

	t.Run("rejects duplicate ID", func(t *testing.T) {
		mgr := setup(t)
		ctx := context.Background()

		_, err := mgr.Add(ctx, "tfl", "default", "")
		require.NoError(t, err)

		_, err = mgr.Add(ctx, "tfl", "default", "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "already exists")
	})

	t.Run("list by channel includes global and scoped", func(t *testing.T) {
		mgr := setup(t)
		ctx := context.Background()

		_, err := mgr.Add(ctx, "google", "work", "admin")
		require.NoError(t, err)
		_, err = mgr.Add(ctx, "tfl", "default", "")
		require.NoError(t, err)
		_, err = mgr.Add(ctx, "monzo", "personal", "finance")
		require.NoError(t, err)

		// "admin" channel should see google/work (scoped) + tfl/default (global).
		sets, err := mgr.ListByChannel(ctx, "admin")
		require.NoError(t, err)
		require.Len(t, sets, 2)

		// "finance" channel should see monzo/personal (scoped) + tfl/default (global).
		sets, err = mgr.ListByChannel(ctx, "finance")
		require.NoError(t, err)
		require.Len(t, sets, 2)
	})
}

func TestManager_Fields(t *testing.T) {
	t.Run("set and get field", func(t *testing.T) {
		mgr := setup(t)
		ctx := context.Background()

		set, err := mgr.Add(ctx, "tfl", "default", "")
		require.NoError(t, err)

		err = mgr.SetField(ctx, set.ID, "api_key", "test-key-123")
		require.NoError(t, err)

		val, err := mgr.GetField(ctx, set.ID, "api_key")
		require.NoError(t, err)
		require.Equal(t, "test-key-123", val)
	})

	t.Run("get nonexistent field returns empty", func(t *testing.T) {
		mgr := setup(t)
		ctx := context.Background()

		set, err := mgr.Add(ctx, "tfl", "default", "")
		require.NoError(t, err)

		val, err := mgr.GetField(ctx, set.ID, "nonexistent")
		require.NoError(t, err)
		require.Empty(t, val)
	})

	t.Run("get all fields", func(t *testing.T) {
		mgr := setup(t)
		ctx := context.Background()

		set, err := mgr.Add(ctx, "monzo", "default", "")
		require.NoError(t, err)

		err = mgr.SetField(ctx, set.ID, "client_id", "id-123")
		require.NoError(t, err)
		err = mgr.SetField(ctx, set.ID, "client_secret", "secret-456")
		require.NoError(t, err)

		fields, err := mgr.GetAllFields(ctx, set.ID, []string{"client_id", "client_secret", "missing"})
		require.NoError(t, err)
		require.Equal(t, "id-123", fields["client_id"])
		require.Equal(t, "secret-456", fields["client_secret"])
		require.NotContains(t, fields, "missing")
	})
}

func TestManager_OAuthTokens(t *testing.T) {
	t.Run("set and get oauth tokens", func(t *testing.T) {
		mgr := setup(t)
		ctx := context.Background()

		set, err := mgr.Add(ctx, "google", "work", "")
		require.NoError(t, err)

		tokens := &credential.OAuthTokens{
			AccessToken:  "access-123",
			RefreshToken: "refresh-456",
		}
		err = mgr.SetOAuthTokens(ctx, set.ID, tokens)
		require.NoError(t, err)

		got, err := mgr.GetOAuthTokens(ctx, set.ID)
		require.NoError(t, err)
		require.Equal(t, "access-123", got.AccessToken)
		require.Equal(t, "refresh-456", got.RefreshToken)
	})

	t.Run("get nonexistent tokens returns nil", func(t *testing.T) {
		mgr := setup(t)
		ctx := context.Background()

		set, err := mgr.Add(ctx, "google", "work", "")
		require.NoError(t, err)

		got, err := mgr.GetOAuthTokens(ctx, set.ID)
		require.NoError(t, err)
		require.Nil(t, got)
	})
}

func TestManager_IsReady(t *testing.T) {
	t.Run("ready when all required fields present", func(t *testing.T) {
		mgr := setup(t)
		ctx := context.Background()

		set, err := mgr.Add(ctx, "tfl", "default", "")
		require.NoError(t, err)

		err = mgr.SetField(ctx, set.ID, "api_key", "key-123")
		require.NoError(t, err)

		ready, err := mgr.IsReady(ctx, set.ID, []string{"api_key"}, false)
		require.NoError(t, err)
		require.True(t, ready)
	})

	t.Run("not ready when field missing", func(t *testing.T) {
		mgr := setup(t)
		ctx := context.Background()

		set, err := mgr.Add(ctx, "tfl", "default", "")
		require.NoError(t, err)

		ready, err := mgr.IsReady(ctx, set.ID, []string{"api_key"}, false)
		require.NoError(t, err)
		require.False(t, ready)
	})

	t.Run("not ready when oauth tokens missing", func(t *testing.T) {
		mgr := setup(t)
		ctx := context.Background()

		set, err := mgr.Add(ctx, "google", "work", "")
		require.NoError(t, err)

		err = mgr.SetField(ctx, set.ID, "client_id", "id")
		require.NoError(t, err)

		ready, err := mgr.IsReady(ctx, set.ID, []string{"client_id"}, true)
		require.NoError(t, err)
		require.False(t, ready)
	})

	t.Run("ready when fields and oauth present", func(t *testing.T) {
		mgr := setup(t)
		ctx := context.Background()

		set, err := mgr.Add(ctx, "google", "work", "")
		require.NoError(t, err)

		err = mgr.SetField(ctx, set.ID, "client_id", "id")
		require.NoError(t, err)
		err = mgr.SetOAuthTokens(ctx, set.ID, &credential.OAuthTokens{AccessToken: "tok"})
		require.NoError(t, err)

		ready, err := mgr.IsReady(ctx, set.ID, []string{"client_id"}, true)
		require.NoError(t, err)
		require.True(t, ready)
	})
}

func TestManager_Remove(t *testing.T) {
	mgr := setup(t)
	ctx := context.Background()

	set, err := mgr.Add(ctx, "tfl", "default", "")
	require.NoError(t, err)

	err = mgr.SetField(ctx, set.ID, "api_key", "key-123")
	require.NoError(t, err)

	err = mgr.Remove(ctx, set.ID)
	require.NoError(t, err)

	got, err := mgr.Get(ctx, set.ID)
	require.NoError(t, err)
	require.Nil(t, got)
}

// --- helpers ---

func setup(t *testing.T) *credential.Manager {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	sec := &memorySecretStore{data: make(map[string]string)}
	return credential.NewManager(s, sec)
}

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
