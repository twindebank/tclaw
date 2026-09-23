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

		_, err := mgr.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{Name: "ha", URL: "https://ha-mcp.example.com/secret", Channels: []string{"desktop"}})
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

		_, err := mgr.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{Name: "ha", URL: "https://ha-mcp.example.com/secret", Channels: []string{"desktop"}})
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

		_, err := mgr.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{Name: "a", URL: "https://a.example.com/x", Channels: []string{"desktop"}})
		require.NoError(t, err)
		_, err = mgr.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{Name: "b", URL: "https://b.example.com/y", Channels: []string{"desktop"}})
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

func TestManager_Instructions(t *testing.T) {
	t.Run("AddRemoteMCP persists instructions", func(t *testing.T) {
		mgr := newManager(t)
		ctx := context.Background()

		_, err := mgr.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{
			Name:         "browser-mcp",
			URL:          "https://browser-mcp.example.com/mcp",
			Channels:     []string{"desktop"},
			Instructions: "One browser session per connection.",
		})
		require.NoError(t, err)

		got, err := mgr.GetRemoteMCP(ctx, "browser-mcp")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, "One browser session per connection.", got.Instructions)
	})

	t.Run("SetInstructions updates an existing entry", func(t *testing.T) {
		mgr := newManager(t)
		ctx := context.Background()

		_, err := mgr.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{
			Name: "browser-mcp", URL: "https://browser-mcp.example.com/mcp", Channels: []string{"desktop"},
		})
		require.NoError(t, err)

		require.NoError(t, mgr.SetInstructions(ctx, "browser-mcp", "Sessions reset each turn."))

		got, err := mgr.GetRemoteMCP(ctx, "browser-mcp")
		require.NoError(t, err)
		require.Equal(t, "Sessions reset each turn.", got.Instructions)
	})

	t.Run("SetInstructions errors on unknown server", func(t *testing.T) {
		mgr := newManager(t)

		err := mgr.SetInstructions(context.Background(), "ghost", "anything")
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
	})
}

func TestManager_ListRemoteMCPs(t *testing.T) {
	t.Run("reads the channels a registration names", func(t *testing.T) {
		mgr, s := newManagerWithStore(t)
		ctx := context.Background()
		writeStored(t, s, `[{"name":"browser-mcp","url":"https://browser-mcp.example.com/mcp","channels":["desktop","email"]}]`)

		mcps, err := mgr.ListRemoteMCPs(ctx)
		require.NoError(t, err)
		require.Len(t, mcps, 1)
		require.Equal(t, []string{"desktop", "email"}, mcps[0].Channels)
	})

	t.Run("reading does not write", func(t *testing.T) {
		mgr, s := newManagerWithStore(t)
		ctx := context.Background()
		const raw = `[{"name":"browser-mcp","url":"https://browser-mcp.example.com/mcp","channels":["desktop"]}]`
		writeStored(t, s, raw)

		_, err := mgr.ListRemoteMCPs(ctx)
		require.NoError(t, err)

		// This load runs on every proxy request, so it must never write.
		got, err := s.Get(ctx, storeKey)
		require.NoError(t, err)
		require.JSONEq(t, raw, string(got))
	})

	t.Run("an empty list is left alone", func(t *testing.T) {
		mgr, s := newManagerWithStore(t)
		ctx := context.Background()
		writeStored(t, s, `[{"name":"browser-mcp","url":"https://browser-mcp.example.com/mcp"}]`)

		mcps, err := mgr.ListRemoteMCPs(ctx)
		require.NoError(t, err)
		require.Len(t, mcps, 1)
		require.Empty(t, mcps[0].Channels, "a server on every channel stays on every channel")
	})
}

func TestManager_SetChannels(t *testing.T) {
	t.Run("replaces the whole list", func(t *testing.T) {
		mgr := newManager(t)
		ctx := context.Background()

		_, err := mgr.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{
			Name: "browser-mcp", URL: "https://browser-mcp.example.com/mcp", Channels: []string{"desktop"},
		})
		require.NoError(t, err)

		require.NoError(t, mgr.SetChannels(ctx, "browser-mcp", []string{"shopping", "email"}))

		got, err := mgr.GetRemoteMCP(ctx, "browser-mcp")
		require.NoError(t, err)
		require.Equal(t, []string{"shopping", "email"}, got.Channels)
	})

	t.Run("leaves other registrations alone", func(t *testing.T) {
		mgr := newManager(t)
		ctx := context.Background()

		_, err := mgr.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{
			Name: "a", URL: "https://a.example.com/mcp", Channels: []string{"desktop"},
		})
		require.NoError(t, err)
		_, err = mgr.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{
			Name: "b", URL: "https://b.example.com/mcp", Channels: []string{"desktop"},
		})
		require.NoError(t, err)

		require.NoError(t, mgr.SetChannels(ctx, "a", []string{"shopping"}))

		got, err := mgr.GetRemoteMCP(ctx, "b")
		require.NoError(t, err)
		require.Equal(t, []string{"desktop"}, got.Channels)
	})

	t.Run("errors on unknown server", func(t *testing.T) {
		mgr := newManager(t)

		err := mgr.SetChannels(context.Background(), "ghost", []string{"desktop"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
	})
}

func TestManager_RemoveChannelFromAll(t *testing.T) {
	t.Run("takes the channel off the registrations that name it", func(t *testing.T) {
		mgr := newManager(t)
		ctx := context.Background()
		addForPrune(t, mgr, "a", "desktop", "shopping")
		addForPrune(t, mgr, "b", "shopping")
		addForPrune(t, mgr, "c", "desktop", "email")

		result, err := mgr.RemoveChannelFromAll(ctx, "desktop")
		require.NoError(t, err)
		require.Equal(t, []string{"a", "c"}, result.Pruned)
		require.Empty(t, result.LeftAlone)

		a, err := mgr.GetRemoteMCP(ctx, "a")
		require.NoError(t, err)
		require.Equal(t, []string{"shopping"}, a.Channels)

		b, err := mgr.GetRemoteMCP(ctx, "b")
		require.NoError(t, err)
		require.Equal(t, []string{"shopping"}, b.Channels, "a registration that never named it is untouched")
	})

	t.Run("leaves a registration scoped only to that channel alone", func(t *testing.T) {
		mgr := newManager(t)
		ctx := context.Background()
		addForPrune(t, mgr, "only", "desktop")

		result, err := mgr.RemoveChannelFromAll(ctx, "desktop")
		require.NoError(t, err)
		require.Equal(t, []string{"only"}, result.LeftAlone)
		require.Empty(t, result.Pruned)

		entry, err := mgr.GetRemoteMCP(ctx, "only")
		require.NoError(t, err)
		require.Equal(t, []string{"desktop"}, entry.Channels,
			"emptying the list would make the server reach every channel, which is a widening")
	})

	t.Run("reports nothing when no registration names the channel", func(t *testing.T) {
		mgr := newManager(t)
		addForPrune(t, mgr, "a", "shopping")

		result, err := mgr.RemoveChannelFromAll(context.Background(), "desktop")
		require.NoError(t, err)
		require.Empty(t, result.Pruned)
		require.Empty(t, result.LeftAlone)
	})

}

// --- helpers ---

func addForPrune(t *testing.T, mgr *remotemcpstore.Manager, name string, channels ...string) {
	t.Helper()
	_, err := mgr.AddRemoteMCP(context.Background(), remotemcpstore.AddRemoteMCPParams{
		Name: name, URL: "https://" + name + ".example.com/mcp", Channels: channels,
	})
	require.NoError(t, err)
}

func newManager(t *testing.T) *remotemcpstore.Manager {
	t.Helper()
	mgr, _ := newManagerWithStore(t)
	return mgr
}

func newManagerWithStore(t *testing.T) (*remotemcpstore.Manager, store.Store) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	secrets := &memorySecretStore{data: map[string]string{}}
	return remotemcpstore.NewManager(s, secrets), s
}

// storeKey is the key the manager keeps its registrations under. Spelled out
// here because these tests assert on the stored shape, not just what is read back.
const storeKey = "remote_mcps"

func writeStored(t *testing.T, s store.Store, raw string) {
	t.Helper()
	require.NoError(t, s.Set(context.Background(), storeKey, []byte(raw)))
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
