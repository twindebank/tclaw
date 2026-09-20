package remotemcpstore_test

import (
	"context"
	"encoding/json"
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
	t.Run("a registration naming a single channel reads as a one-element list", func(t *testing.T) {
		mgr, s := newManagerWithStore(t)
		ctx := context.Background()
		writeStored(t, s, `[{"name":"browser-mcp","url":"https://browser-mcp.example.com/mcp","channel":"desktop"}]`)

		mcps, err := mgr.ListRemoteMCPs(ctx)
		require.NoError(t, err)
		require.Len(t, mcps, 1)
		require.Equal(t, []string{"desktop"}, mcps[0].Channels, "the single channel should become the list")
	})

	t.Run("reading does not rewrite the store", func(t *testing.T) {
		mgr, s := newManagerWithStore(t)
		ctx := context.Background()
		writeStored(t, s, `[{"name":"browser-mcp","url":"https://browser-mcp.example.com/mcp","channel":"desktop"}]`)

		_, err := mgr.ListRemoteMCPs(ctx)
		require.NoError(t, err)

		stored := readStored(t, s)
		require.Contains(t, stored[0], "channel",
			"a read resolves the shape in memory; only MigrateChannelScope writes")
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

func TestManager_MigrateChannelScope(t *testing.T) {
	t.Run("rewrites a single-channel registration keeping every other field", func(t *testing.T) {
		mgr, s := newManagerWithStore(t)
		writeStored(t, s, `[{
			"name":"browser-mcp",
			"url":"https://browser-mcp.example.com/mcp",
			"channel":"desktop",
			"created_at":"2026-01-02T03:04:05Z",
			"url_sensitive":true,
			"tool_names":["browser_navigate","browser_click"],
			"tls_pin_sha256":"5675bf78",
			"instructions":"One session at a time."
		}]`)

		require.NoError(t, mgr.MigrateChannelScope(context.Background()))

		stored := readStored(t, s)
		require.Len(t, stored, 1)
		require.Equal(t, []any{"desktop"}, stored[0]["channels"])
		require.NotContains(t, stored[0], "channel", "the single-channel key should be gone once rewritten")

		// The rewrite overwrites the only copy of this data, so nothing may be
		// dropped on the way through.
		require.Equal(t, "browser-mcp", stored[0]["name"])
		require.Equal(t, "https://browser-mcp.example.com/mcp", stored[0]["url"])
		require.Equal(t, "2026-01-02T03:04:05Z", stored[0]["created_at"])
		require.Equal(t, true, stored[0]["url_sensitive"])
		require.Equal(t, []any{"browser_navigate", "browser_click"}, stored[0]["tool_names"])
		require.Equal(t, "5675bf78", stored[0]["tls_pin_sha256"])
		require.Equal(t, "One session at a time.", stored[0]["instructions"])
	})

	t.Run("leaves a store that is already a list untouched", func(t *testing.T) {
		mgr, s := newManagerWithStore(t)
		const raw = `[{"name":"browser-mcp","url":"https://browser-mcp.example.com/mcp","channels":["desktop"]}]`
		writeStored(t, s, raw)

		require.NoError(t, mgr.MigrateChannelScope(context.Background()))

		got, err := s.Get(context.Background(), storeKey)
		require.NoError(t, err)
		require.JSONEq(t, raw, string(got), "nothing to migrate means nothing written")
	})

	t.Run("an empty store is not an error", func(t *testing.T) {
		mgr, _ := newManagerWithStore(t)
		require.NoError(t, mgr.MigrateChannelScope(context.Background()))
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

// --- helpers ---

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

func readStored(t *testing.T, s store.Store) []map[string]any {
	t.Helper()
	raw, err := s.Get(context.Background(), storeKey)
	require.NoError(t, err)
	var stored []map[string]any
	require.NoError(t, json.Unmarshal(raw, &stored))
	return stored
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
