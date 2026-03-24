package telegramclient_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
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

func TestCreateBot(t *testing.T) {
	t.Run("rejects empty purpose", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_create_bot", map[string]any{
			"purpose": "",
		})
		require.Contains(t, err.Error(), "purpose")
	})

	t.Run("rejects when no credentials stored", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_create_bot", map[string]any{
			"purpose": "assistant",
		})
		require.Contains(t, err.Error(), "credentials not configured")
	})
}

func TestDeleteBot(t *testing.T) {
	t.Run("rejects empty username", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "telegram_client_delete_bot", map[string]any{
			"username": "",
		})
		require.Contains(t, err.Error(), "username")
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

func TestSessionStorage(t *testing.T) {
	t.Run("round-trip store and load", func(t *testing.T) {
		secrets := &memorySecretStore{data: map[string]string{}}
		storage := telegramclient.NewSecretSessionStorageForTest(secrets)

		original := []byte("test session data with binary \x00\x01\x02\xff")

		err := storage.StoreSession(context.Background(), original)
		require.NoError(t, err)

		// Verify it's stored as base64 in the secret store.
		raw := secrets.data[telegramclient.SessionStoreKey]
		require.NotEmpty(t, raw)
		decoded, err := base64.StdEncoding.DecodeString(raw)
		require.NoError(t, err)
		require.Equal(t, original, decoded)

		// Load it back.
		loaded, err := storage.LoadSession(context.Background())
		require.NoError(t, err)
		require.Equal(t, original, loaded)
	})

	t.Run("load returns nil when no session exists", func(t *testing.T) {
		secrets := &memorySecretStore{data: map[string]string{}}
		storage := telegramclient.NewSecretSessionStorageForTest(secrets)

		loaded, err := storage.LoadSession(context.Background())
		require.NoError(t, err)
		require.Nil(t, loaded)
	})

	t.Run("load rejects corrupt base64", func(t *testing.T) {
		secrets := &memorySecretStore{data: map[string]string{
			telegramclient.SessionStoreKey: "not-valid-base64!!!",
		}}
		storage := telegramclient.NewSecretSessionStorageForTest(secrets)

		_, err := storage.LoadSession(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "decode session")
	})
}

func TestBotNameGeneration(t *testing.T) {
	t.Run("generates valid names", func(t *testing.T) {
		username, displayName, err := telegramclient.GenerateBotNamesForTest("assistant")
		require.NoError(t, err)

		// Username: tclaw_<8hex>_bot
		require.True(t, strings.HasPrefix(username, "tclaw_"), "username should start with tclaw_: %s", username)
		require.True(t, strings.HasSuffix(username, "_bot"), "username should end with _bot: %s", username)
		require.Len(t, username, len("tclaw_12345678_bot"))

		// Display name: tclaw · <purpose>
		require.Equal(t, "tclaw · assistant", displayName)
	})

	t.Run("generates unique names", func(t *testing.T) {
		u1, _, err := telegramclient.GenerateBotNamesForTest("test")
		require.NoError(t, err)
		u2, _, err := telegramclient.GenerateBotNamesForTest("test")
		require.NoError(t, err)

		// Extremely unlikely to collide with 4 random bytes.
		require.NotEqual(t, u1, u2)
	})
}

func TestTokenRegex(t *testing.T) {
	t.Run("extracts token from BotFather response", func(t *testing.T) {
		response := `Done! Congratulations on your new bot. You will find it at t.me/tclaw_a3f7b21e_bot. You can now add a description, about section and profile picture for your bot, see /help for a list of commands. By the way, when you've finished creating your cool bot, ping our Bot Support if you want a better username for it. Just make sure the bot is fully functional before you do this.

Use this token to access the HTTP API:
7123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw

Keep your token secure and store it safely, it can be used by anyone to control your bot.`

		token := telegramclient.ExtractTokenForTest(response)
		require.Equal(t, "7123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw", token)
	})

	t.Run("returns empty for no token", func(t *testing.T) {
		token := telegramclient.ExtractTokenForTest("Sorry, this username is already taken.")
		require.Empty(t, token)
	})
}

func TestContainsError(t *testing.T) {
	t.Run("detects sorry", func(t *testing.T) {
		require.True(t, telegramclient.ContainsErrorForTest("Sorry, this username is already taken."))
	})

	t.Run("detects error", func(t *testing.T) {
		require.True(t, telegramclient.ContainsErrorForTest("An error occurred while processing your request."))
	})

	t.Run("detects invalid", func(t *testing.T) {
		require.True(t, telegramclient.ContainsErrorForTest("Invalid username format."))
	})

	t.Run("detects can't", func(t *testing.T) {
		require.True(t, telegramclient.ContainsErrorForTest("I can't find that bot."))
	})

	t.Run("passes normal response", func(t *testing.T) {
		require.False(t, telegramclient.ContainsErrorForTest("Done! Congratulations on your new bot."))
	})

	t.Run("passes token response", func(t *testing.T) {
		require.False(t, telegramclient.ContainsErrorForTest("Use this token to access the HTTP API:\n7123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"))
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
		"telegram_client_create_bot",
		"telegram_client_delete_bot",
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
