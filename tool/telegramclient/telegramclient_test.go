package telegramclient_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/tool/telegramclient"
)

func TestSetup(t *testing.T) {
	t.Run("stores valid credentials", func(t *testing.T) {
		h, secrets := setup(t)

		result := callTool(t, h, "telegram_client_setup", map[string]any{
			"api_id":   12345,
			"api_hash": "abcdef0123456789",
		})

		var got map[string]string
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "stored", got["status"])

		require.Equal(t, "12345", secrets.data[telegramclient.APIIDStoreKey])
		require.Equal(t, "abcdef0123456789", secrets.data[telegramclient.APIHashStoreKey])
	})

	t.Run("rejects zero api_id", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_setup", map[string]any{
			"api_id":   0,
			"api_hash": "abcdef0123456789",
		})
		require.Contains(t, err.Error(), "api_id")
	})

	t.Run("rejects empty api_hash", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_setup", map[string]any{
			"api_id":   12345,
			"api_hash": "",
		})
		require.Contains(t, err.Error(), "api_hash")
	})
}

func TestStatus(t *testing.T) {
	t.Run("no credentials", func(t *testing.T) {
		h, _ := setup(t)

		result := callTool(t, h, "telegram_client_status", map[string]any{})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, false, got["credentials_stored"])
		require.Equal(t, "", got["phone"])
		require.Equal(t, false, got["connected"])
	})

	t.Run("with stored credentials and phone", func(t *testing.T) {
		h, secrets := setup(t)

		secrets.data[telegramclient.APIIDStoreKey] = "12345"
		secrets.data[telegramclient.APIHashStoreKey] = "abcdef"
		secrets.data[telegramclient.PhoneStoreKey] = "+447000000000"

		result := callTool(t, h, "telegram_client_status", map[string]any{})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, true, got["credentials_stored"])
		require.Equal(t, "+447000000000", got["phone"])
		// Not connected since we haven't called auth.
		require.Equal(t, false, got["connected"])
	})
}

func TestAuth(t *testing.T) {
	t.Run("rejects empty phone", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_auth", map[string]any{
			"phone": "",
		})
		require.Contains(t, err.Error(), "phone")
	})

	t.Run("rejects when no credentials stored", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_auth", map[string]any{
			"phone": "+447000000000",
		})
		require.Contains(t, err.Error(), "credentials not configured")
	})
}

func TestVerify(t *testing.T) {
	t.Run("rejects empty code", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_verify", map[string]any{
			"code": "",
		})
		require.Contains(t, err.Error(), "code")
	})

	t.Run("rejects when no pending auth", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_verify", map[string]any{
			"code": "12345",
		})
		require.Contains(t, err.Error(), "no pending auth")
	})
}

func TestTwoFA(t *testing.T) {
	t.Run("rejects empty password", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_2fa", map[string]any{
			"password": "",
		})
		require.Contains(t, err.Error(), "password")
	})

	t.Run("rejects when no credentials stored", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_2fa", map[string]any{
			"password": "mysecretpassword",
		})
		require.Contains(t, err.Error(), "credentials not configured")
	})
}

func TestConfigureBot(t *testing.T) {
	t.Run("rejects empty username", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_configure_bot", map[string]any{
			"username": "",
		})
		require.Contains(t, err.Error(), "username")
	})
}

func TestCreateGroup(t *testing.T) {
	t.Run("rejects empty title", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_create_group", map[string]any{
			"title": "",
		})
		require.Contains(t, err.Error(), "title")
	})

	t.Run("rejects when no credentials stored", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_create_group", map[string]any{
			"title": "Test Group",
		})
		require.Contains(t, err.Error(), "credentials not configured")
	})
}

func TestListChats(t *testing.T) {
	t.Run("rejects when no credentials stored", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_list_chats", map[string]any{})
		require.Contains(t, err.Error(), "credentials not configured")
	})
}

func TestGetHistory(t *testing.T) {
	t.Run("rejects zero chat_id", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_get_history", map[string]any{
			"chat_id": 0,
		})
		require.Contains(t, err.Error(), "chat_id")
	})

	t.Run("rejects when no credentials stored", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_get_history", map[string]any{
			"chat_id": 12345,
		})
		require.Contains(t, err.Error(), "credentials not configured")
	})
}

func TestSearch(t *testing.T) {
	t.Run("rejects empty query", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_search", map[string]any{
			"query": "",
		})
		require.Contains(t, err.Error(), "query")
	})

	t.Run("rejects when no credentials stored", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_search", map[string]any{
			"query": "hello",
		})
		require.Contains(t, err.Error(), "credentials not configured")
	})
}

func TestAllToolsRegistered(t *testing.T) {
	h, _ := setup(t)

	expectedTools := []string{
		"telegram_client_setup",
		"telegram_client_auth",
		"telegram_client_verify",
		"telegram_client_2fa",
		"telegram_client_status",
		"telegram_client_configure_bot",
		"telegram_client_create_group",
		"telegram_client_list_chats",
		"telegram_client_get_history",
		"telegram_client_search",
	}

	tools := h.ListTools()
	registeredNames := make(map[string]bool, len(tools))
	for _, tool := range tools {
		registeredNames[tool.Name] = true
	}

	for _, name := range expectedTools {
		require.True(t, registeredNames[name], "tool %q not registered", name)
	}
}

func TestSetupThenStatus(t *testing.T) {
	t.Run("setup credentials then status reflects them", func(t *testing.T) {
		h, _ := setup(t)

		// Initially no credentials.
		result := callTool(t, h, "telegram_client_status", map[string]any{})
		var status map[string]any
		require.NoError(t, json.Unmarshal(result, &status))
		require.Equal(t, false, status["credentials_stored"])

		// Store credentials.
		callTool(t, h, "telegram_client_setup", map[string]any{
			"api_id":   99999,
			"api_hash": "deadbeef",
		})

		// Now status should show stored.
		result = callTool(t, h, "telegram_client_status", map[string]any{})
		require.NoError(t, json.Unmarshal(result, &status))
		require.Equal(t, true, status["credentials_stored"])
	})
}

// --- helpers ---

func setup(t *testing.T) (*mcp.Handler, *memorySecretStore) {
	t.Helper()
	secrets := &memorySecretStore{data: map[string]string{}}
	stateStore, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	handler := mcp.NewHandler()
	telegramclient.RegisterTools(handler, telegramclient.Deps{
		SecretStore: secrets,
		StateStore:  stateStore,
	})

	return handler, secrets
}

func callTool(t *testing.T, h *mcp.Handler, name string, args any) json.RawMessage {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	result, err := h.Call(context.Background(), name, argsJSON)
	require.NoError(t, err, "call %s", name)
	return result
}

func callToolExpectError(t *testing.T, h *mcp.Handler, name string, args any) error {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = h.Call(context.Background(), name, argsJSON)
	require.Error(t, err, "expected error from %s", name)
	return err
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
