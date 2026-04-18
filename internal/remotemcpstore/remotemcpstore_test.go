package remotemcpstore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/remotemcpstore"
)

func TestManager_StaticHeadersRoundtrip(t *testing.T) {
	t.Run("Set then Get preserves headers", func(t *testing.T) {
		mgr := newManager(t)
		ctx := context.Background()

		_, err := mgr.AddRemoteMCP(ctx, "ha", "https://ha-mcp.example.com/secret", "desktop")
		require.NoError(t, err)

		err = mgr.SetRemoteMCPAuth(ctx, "ha", &remotemcpstore.RemoteMCPAuth{
			StaticHeaders: map[string]string{
				"CF-Access-Client-Id":     "client-id",
				"CF-Access-Client-Secret": "super-secret",
			},
		})
		require.NoError(t, err)

		got, err := mgr.GetRemoteMCPAuth(ctx, "ha")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, "client-id", got.StaticHeaders["CF-Access-Client-Id"])
		require.Equal(t, "super-secret", got.StaticHeaders["CF-Access-Client-Secret"])
		require.Empty(t, got.AccessToken, "static-only auth should not have a bearer token")
	})

	t.Run("Remove deletes auth including static headers", func(t *testing.T) {
		mgr := newManager(t)
		ctx := context.Background()

		_, err := mgr.AddRemoteMCP(ctx, "ha", "https://ha-mcp.example.com/secret", "desktop")
		require.NoError(t, err)
		err = mgr.SetRemoteMCPAuth(ctx, "ha", &remotemcpstore.RemoteMCPAuth{
			StaticHeaders: map[string]string{"X-Foo": "bar"},
		})
		require.NoError(t, err)

		require.NoError(t, mgr.RemoveRemoteMCP(ctx, "ha"))

		auth, err := mgr.GetRemoteMCPAuth(ctx, "ha")
		require.NoError(t, err)
		require.Nil(t, auth, "auth entry should be gone after remove")

		mcps, err := mgr.ListRemoteMCPs(ctx)
		require.NoError(t, err)
		require.Empty(t, mcps)
	})

	t.Run("multiple remotes keep distinct headers", func(t *testing.T) {
		mgr := newManager(t)
		ctx := context.Background()

		_, err := mgr.AddRemoteMCP(ctx, "a", "https://a.example.com/x", "desktop")
		require.NoError(t, err)
		_, err = mgr.AddRemoteMCP(ctx, "b", "https://b.example.com/y", "desktop")
		require.NoError(t, err)

		require.NoError(t, mgr.SetRemoteMCPAuth(ctx, "a", &remotemcpstore.RemoteMCPAuth{
			StaticHeaders: map[string]string{"X-Tenant": "alpha"},
		}))
		require.NoError(t, mgr.SetRemoteMCPAuth(ctx, "b", &remotemcpstore.RemoteMCPAuth{
			StaticHeaders: map[string]string{"X-Tenant": "beta"},
		}))

		authA, err := mgr.GetRemoteMCPAuth(ctx, "a")
		require.NoError(t, err)
		authB, err := mgr.GetRemoteMCPAuth(ctx, "b")
		require.NoError(t, err)

		require.Equal(t, "alpha", authA.StaticHeaders["X-Tenant"])
		require.Equal(t, "beta", authB.StaticHeaders["X-Tenant"])

		// Removing one must not affect the other.
		require.NoError(t, mgr.RemoveRemoteMCP(ctx, "a"))
		authB2, err := mgr.GetRemoteMCPAuth(ctx, "b")
		require.NoError(t, err)
		require.Equal(t, "beta", authB2.StaticHeaders["X-Tenant"])
	})
}

// --- helpers ---

func newManager(t *testing.T) *remotemcpstore.Manager {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	secrets := &memorySecretStore{data: map[string]string{}}
	return remotemcpstore.NewManager(s, secrets)
}

type memorySecretStore struct {
	data map[string]string
}

var _ secret.Store = (*memorySecretStore)(nil)

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
