package channeltools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/config"
	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/tool/channeltools"
)

func TestChannelList(t *testing.T) {
	t.Run("shows static channels", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		result := callTool(t, th.handler, "channel_list", map[string]any{})

		var entries []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		}
		require.NoError(t, json.Unmarshal(result, &entries))
		require.Len(t, entries, 1)
		require.Equal(t, "desktop", entries[0].Name)
		require.Equal(t, "static", entries[0].Source)
	})
}

func TestChannelCreate(t *testing.T) {
	t.Run("socket in local env", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		result := callTool(t, th.handler, "channel_create", map[string]any{
			"name":        "phone",
			"description": "Mobile device",
			"type":        "socket",
		})

		var created map[string]any
		require.NoError(t, json.Unmarshal(result, &created))
		require.Equal(t, "phone", created["name"])
		require.Equal(t, "socket", created["type"])

		// Should appear in list alongside the static channel.
		listResult := callTool(t, th.handler, "channel_list", map[string]any{})
		var entries []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		}
		require.NoError(t, json.Unmarshal(listResult, &entries))
		require.Len(t, entries, 2)
	})

	t.Run("socket blocked in prod", func(t *testing.T) {
		th := setupHarness(t, config.EnvProd)

		err := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":        "phone",
			"description": "Mobile device",
			"type":        "socket",
		})
		require.Contains(t, err.Error(), "not allowed")
	})

	t.Run("telegram stores token in secret store", func(t *testing.T) {
		th := setupHarness(t, config.EnvProd)

		result := callTool(t, th.handler, "channel_create", map[string]any{
			"name":        "mybot",
			"description": "Personal Telegram bot",
			"type":        "telegram",
			"telegram_config": map[string]any{
				"token":         "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11",
				"allowed_users": []any{float64(123456789)},
			},
		})

		var created map[string]any
		require.NoError(t, json.Unmarshal(result, &created))
		require.Equal(t, "mybot", created["name"])
		require.Equal(t, "telegram", created["type"])

		// Token should be in the secret store, not in the dynamic config.
		cfg, err := th.dynamicStore.Get(context.Background(), "mybot")
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.Equal(t, channel.TypeTelegram, cfg.Type)

		token, err := th.secretStore.Get(context.Background(), channel.ChannelSecretKey("mybot"))
		require.NoError(t, err)
		require.Equal(t, "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11", token)
	})

	t.Run("telegram missing config", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		err := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":        "mybot",
			"description": "Missing token",
			"type":        "telegram",
		})
		require.Contains(t, err.Error(), "telegram_config")
	})

	t.Run("telegram empty token", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		err := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":        "mybot",
			"description": "Empty token",
			"type":        "telegram",
			"telegram_config": map[string]any{
				"token": "",
			},
		})
		require.Contains(t, err.Error(), "telegram_config")
	})

	t.Run("rejects static name collision", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		err := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":        "desktop",
			"description": "conflicts with static",
			"type":        "socket",
		})
		require.Contains(t, err.Error(), "static channel")
	})

	t.Run("rejects duplicate dynamic name", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		callTool(t, th.handler, "channel_create", map[string]any{
			"name":        "phone",
			"description": "first",
			"type":        "socket",
		})

		err := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":        "phone",
			"description": "duplicate",
			"type":        "socket",
		})
		require.Contains(t, err.Error(), "already exists")
	})
}

func TestChannelEdit(t *testing.T) {
	t.Run("updates description", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "phone", "description": "Old description", "type": "socket",
		})

		callTool(t, th.handler, "channel_edit", map[string]any{
			"name":        "phone",
			"description": "New description",
		})

		cfg, err := th.dynamicStore.Get(context.Background(), "phone")
		require.NoError(t, err)
		require.Equal(t, "New description", cfg.Description)
	})

	t.Run("rotates telegram token", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "mybot", "description": "Telegram bot", "type": "telegram",
			"telegram_config": map[string]any{"token": "old-token", "allowed_users": []any{float64(123456789)}},
		})

		result := callTool(t, th.handler, "channel_edit", map[string]any{
			"name":            "mybot",
			"telegram_config": map[string]any{"token": "new-token"},
		})

		var edited map[string]any
		require.NoError(t, json.Unmarshal(result, &edited))
		require.Equal(t, true, edited["token_rotated"])

		token, err := th.secretStore.Get(context.Background(), channel.ChannelSecretKey("mybot"))
		require.NoError(t, err)
		require.Equal(t, "new-token", token)
	})

	t.Run("telegram config on socket channel errors", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "phone", "description": "Socket", "type": "socket",
		})

		err := callToolExpectError(t, th.handler, "channel_edit", map[string]any{
			"name":            "phone",
			"telegram_config": map[string]any{"token": "wrong-type"},
		})
		require.Contains(t, err.Error(), "telegram channels")
	})

	t.Run("rejects static channel", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		err := callToolExpectError(t, th.handler, "channel_edit", map[string]any{
			"name":        "desktop",
			"description": "try to edit static",
		})
		require.Contains(t, err.Error(), "static channel")
	})

	t.Run("requires at least one field", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "phone", "description": "Socket", "type": "socket",
		})

		err := callToolExpectError(t, th.handler, "channel_edit", map[string]any{
			"name": "phone",
		})
		require.Contains(t, err.Error(), "at least one")
	})
}

func TestChannelChangeCallback(t *testing.T) {
	t.Run("create calls OnChannelChange", func(t *testing.T) {
		var called int
		th := setupHarnessWithCallback(t, config.EnvLocal, func() { called++ })

		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "test", "description": "Test channel", "type": "socket",
		})
		require.Equal(t, 1, called)
	})

	t.Run("edit calls OnChannelChange", func(t *testing.T) {
		var called int
		th := setupHarnessWithCallback(t, config.EnvLocal, func() { called++ })

		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "test", "description": "Test channel", "type": "socket",
		})
		called = 0

		callTool(t, th.handler, "channel_edit", map[string]any{
			"name": "test", "description": "Updated",
		})
		require.Equal(t, 1, called)
	})

	t.Run("delete calls OnChannelChange", func(t *testing.T) {
		var called int
		th := setupHarnessWithCallback(t, config.EnvLocal, func() { called++ })

		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "test", "description": "Test channel", "type": "socket",
		})
		called = 0

		callTool(t, th.handler, "channel_delete", map[string]any{"name": "test"})
		require.Equal(t, 1, called)
	})

	t.Run("nil callback does not panic", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "test", "description": "Test channel", "type": "socket",
		})
	})
}

func TestChannelDelete(t *testing.T) {
	t.Run("removes dynamic channel", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "phone", "description": "will be deleted", "type": "socket",
		})

		callTool(t, th.handler, "channel_delete", map[string]any{"name": "phone"})

		cfg, err := th.dynamicStore.Get(context.Background(), "phone")
		require.NoError(t, err)
		require.Nil(t, cfg)
	})

	t.Run("cleans up telegram secret", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "mybot", "description": "Telegram bot", "type": "telegram",
			"telegram_config": map[string]any{"token": "secret-token", "allowed_users": []any{float64(123456789)}},
		})

		// Verify secret exists before delete.
		token, err := th.secretStore.Get(context.Background(), channel.ChannelSecretKey("mybot"))
		require.NoError(t, err)
		require.Equal(t, "secret-token", token)

		callTool(t, th.handler, "channel_delete", map[string]any{"name": "mybot"})

		// Both config and secret should be gone.
		token, err = th.secretStore.Get(context.Background(), channel.ChannelSecretKey("mybot"))
		require.NoError(t, err)
		require.Empty(t, token)

		cfg, err := th.dynamicStore.Get(context.Background(), "mybot")
		require.NoError(t, err)
		require.Nil(t, cfg)
	})

	t.Run("rejects static channel", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		err := callToolExpectError(t, th.handler, "channel_delete", map[string]any{"name": "desktop"})
		require.Contains(t, err.Error(), "static channel")
	})

	t.Run("rejects nonexistent channel", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		err := callToolExpectError(t, th.handler, "channel_delete", map[string]any{"name": "nonexistent"})
		require.Contains(t, err.Error(), "not found")
	})
}

// --- helpers ---

type testHarness struct {
	handler      *mcp.Handler
	dynamicStore *channel.DynamicStore
	secretStore  *memorySecretStore
}

func setupHarness(t *testing.T, env config.Env) testHarness {
	return setupHarnessWithCallback(t, env, nil)
}

func setupHarnessWithCallback(t *testing.T, env config.Env, onChange func()) testHarness {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	dynamicStore := channel.NewDynamicStore(s)
	handler := mcp.NewHandler()
	secrets := newMemorySecretStore()

	staticEntries := []channel.RegistryEntry{
		{Info: channel.Info{ID: "/tmp/test/desktop.sock", Type: channel.TypeSocket, Name: "desktop", Description: "Desktop workstation", Source: channel.SourceStatic}},
	}
	registry := channel.NewRegistry(staticEntries, dynamicStore)

	channeltools.RegisterTools(handler, channeltools.Deps{
		Registry:        registry,
		Env:             env,
		SecretStore:     secrets,
		OnChannelChange: onChange,
	})

	return testHarness{handler: handler, dynamicStore: dynamicStore, secretStore: secrets}
}

func callTool(t *testing.T, h *mcp.Handler, name string, args any) json.RawMessage {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	result, err := h.Call(context.Background(), name, argsJSON)
	require.NoError(t, err)
	return result
}

func callToolExpectError(t *testing.T, h *mcp.Handler, name string, args any) error {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = h.Call(context.Background(), name, argsJSON)
	require.Error(t, err)
	return err
}

// memorySecretStore is an in-memory secret.Store for testing.
type memorySecretStore struct {
	data map[string]string
}

func newMemorySecretStore() *memorySecretStore {
	return &memorySecretStore{data: make(map[string]string)}
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
