package bankingtools_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/tool/bankingtools"
)

func TestSetCredentials(t *testing.T) {
	t.Run("stores valid credentials", func(t *testing.T) {
		h, secrets, _ := setup(t)

		result := callTool(t, h, "banking_set_credentials", map[string]any{
			"application_id":  "test-app-id",
			"private_key_pem": testPrivateKeyPEM(t),
		})

		var got map[string]string
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "stored", got["status"])

		require.Equal(t, "test-app-id", secrets.data[bankingtools.ApplicationIDStoreKey])
		require.NotEmpty(t, secrets.data[bankingtools.PrivateKeyStoreKey])
	})

	t.Run("returns credential error when store empty and no params", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "banking_set_credentials", map[string]any{})
		require.Contains(t, err.Error(), "CREDENTIALS_NEEDED")
	})

	t.Run("reads from store when called with no params", func(t *testing.T) {
		h, secrets, _ := setup(t)
		pemKey := testPrivateKeyPEM(t)
		secrets.data[bankingtools.ApplicationIDStoreKey] = "stored-app-id"
		secrets.data[bankingtools.PrivateKeyStoreKey] = pemKey

		result := callTool(t, h, "banking_set_credentials", map[string]any{})

		var got map[string]string
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "stored", got["status"])
	})

	t.Run("returns credential error when only one field in store", func(t *testing.T) {
		h, secrets, _ := setup(t)
		secrets.data[bankingtools.ApplicationIDStoreKey] = "stored-app-id"

		err := callToolExpectError(t, h, "banking_set_credentials", map[string]any{})
		require.Contains(t, err.Error(), "CREDENTIALS_NEEDED")
	})

	t.Run("rejects invalid PEM", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "banking_set_credentials", map[string]any{
			"application_id":  "test-app-id",
			"private_key_pem": "not-a-valid-pem",
		})
		require.Contains(t, err.Error(), "PEM")
	})
}

func TestListAccounts(t *testing.T) {
	t.Run("returns empty when no banks connected", func(t *testing.T) {
		h, _, _ := setup(t)

		result := callTool(t, h, "banking_list_accounts", map[string]any{})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.NotNil(t, got["accounts"])
		require.Len(t, got["accounts"], 0)
		require.Contains(t, got["message"], "No banks connected")
	})

	t.Run("lists accounts from stored sessions", func(t *testing.T) {
		h, _, stateStore := setup(t)

		// Seed a session directly in the state store.
		sessions := []map[string]any{
			{
				"session_id":  "sess-1",
				"bank_name":   "Monzo",
				"aspsp_id":    "Monzo",
				"country":     "GB",
				"account_ids": []string{"acc-1", "acc-2"},
				"valid_until": "2027-01-01T00:00:00Z",
				"created_at":  "2026-01-01T00:00:00Z",
			},
		}
		data, err := json.Marshal(sessions)
		require.NoError(t, err)
		require.NoError(t, stateStore.Set(context.Background(), "banking_sessions", data))

		result := callTool(t, h, "banking_list_accounts", map[string]any{})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		accounts := got["accounts"].([]any)
		require.Len(t, accounts, 2)

		first := accounts[0].(map[string]any)
		require.Equal(t, "acc-1", first["account_id"])
		require.Equal(t, "Monzo", first["bank_name"])
	})

	t.Run("flags expired sessions", func(t *testing.T) {
		h, _, stateStore := setup(t)

		sessions := []map[string]any{
			{
				"session_id":  "sess-expired",
				"bank_name":   "HSBC",
				"aspsp_id":    "HSBC",
				"country":     "GB",
				"account_ids": []string{"acc-expired"},
				"valid_until": "2020-01-01T00:00:00Z",
				"created_at":  "2019-10-01T00:00:00Z",
			},
		}
		data, err := json.Marshal(sessions)
		require.NoError(t, err)
		require.NoError(t, stateStore.Set(context.Background(), "banking_sessions", data))

		result := callTool(t, h, "banking_list_accounts", map[string]any{})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))

		accounts := got["accounts"].([]any)
		require.Len(t, accounts, 1)
		first := accounts[0].(map[string]any)
		require.Equal(t, true, first["expired"])

		expiredBanks := got["expired_banks"].([]any)
		require.Contains(t, expiredBanks, "HSBC")
	})
}

func TestGetBalance(t *testing.T) {
	t.Run("rejects missing account_id", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "banking_get_balance", map[string]any{
			"account_id": "",
		})
		require.Contains(t, err.Error(), "account_id")
	})

	t.Run("rejects unknown account", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "banking_get_balance", map[string]any{
			"account_id": "nonexistent",
		})
		require.Contains(t, err.Error(), "no banking session found")
	})

	t.Run("rejects expired session", func(t *testing.T) {
		h, _, stateStore := setup(t)

		sessions := []map[string]any{
			{
				"session_id":  "sess-expired",
				"bank_name":   "Barclays",
				"aspsp_id":    "Barclays",
				"country":     "GB",
				"account_ids": []string{"acc-bar-1"},
				"valid_until": "2020-01-01T00:00:00Z",
				"created_at":  "2019-10-01T00:00:00Z",
			},
		}
		data, err := json.Marshal(sessions)
		require.NoError(t, err)
		require.NoError(t, stateStore.Set(context.Background(), "banking_sessions", data))

		err = callToolExpectError(t, h, "banking_get_balance", map[string]any{
			"account_id": "acc-bar-1",
		})
		require.Contains(t, err.Error(), "expired")
		require.Contains(t, err.Error(), "Barclays")
	})
}

func TestGetTransactions(t *testing.T) {
	t.Run("rejects missing account_id", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "banking_get_transactions", map[string]any{
			"account_id": "",
		})
		require.Contains(t, err.Error(), "account_id")
	})
}

func TestListBanks(t *testing.T) {
	t.Run("rejects when no credentials", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "banking_list_banks", map[string]any{})
		require.Contains(t, err.Error(), "credentials not configured")
	})
}

func TestConnect(t *testing.T) {
	t.Run("rejects when no credentials", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "banking_connect", map[string]any{
			"aspsp_name": "Monzo",
		})
		// No callback server set, so it fails before credential check.
		require.Contains(t, err.Error(), "callback server not available")
	})

	t.Run("rejects missing aspsp_name", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "banking_connect", map[string]any{
			"aspsp_name": "",
		})
		require.Contains(t, err.Error(), "aspsp_name")
	})
}

func TestAuthWait(t *testing.T) {
	t.Run("rejects when no pending flow", func(t *testing.T) {
		h, _, _ := setup(t)

		err := callToolExpectError(t, h, "banking_auth_wait", map[string]any{
			"aspsp_name": "Monzo",
		})
		require.Contains(t, err.Error(), "no pending authorization")
	})
}

func TestClientJWT(t *testing.T) {
	t.Run("creates client from valid PEM", func(t *testing.T) {
		client, err := bankingtools.NewClient("test-app", testPrivateKeyPEM(t))
		require.NoError(t, err)
		require.NotNil(t, client)
	})

	t.Run("rejects invalid PEM", func(t *testing.T) {
		_, err := bankingtools.NewClient("test-app", "not-a-pem")
		require.Error(t, err)
		require.Contains(t, err.Error(), "PEM")
	})

	t.Run("rejects non-RSA PEM", func(t *testing.T) {
		// An EC key in PEM format — parsed but not RSA.
		ecPEM := "-----BEGIN EC PRIVATE KEY-----\nMHQCAQEEIBkg4LVWM9nuwNSk3yByxZpYRTBnVjGNwQjm3bGTLSRUoAcGBSuBBAAi\noWQDYgAEY1GlPyRPrzIaXnFGia2MkeaD/LPIBcSTJEn4oSxwkDNCi6pCjKXS/pgQ\nBi1gOTNOMIqOJeEzR0+HPxvBRUdPkfl2HBIZiMrcSu7OBsYxi4kKMhgFrEQ/TlYs\nTpljlqdi\n-----END EC PRIVATE KEY-----\n"
		_, err := bankingtools.NewClient("test-app", ecPEM)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse private key")
	})
}

// --- helpers ---

func setup(t *testing.T) (*mcp.Handler, *memorySecretStore, store.Store) {
	t.Helper()
	secrets := &memorySecretStore{data: map[string]string{}}
	stateStore, err := store.NewFS(t.TempDir())
	require.NoError(t, err)

	handler := mcp.NewHandler()
	deps := bankingtools.Deps{
		SecretStore: secrets,
		StateStore:  stateStore,
		Callback:    nil, // No callback server in tests.
	}
	bankingtools.RegisterInfoTools(handler, deps)
	bankingtools.RegisterTools(handler, deps)

	return handler, secrets, stateStore
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

// testPrivateKeyPEM generates a fresh RSA private key in PEM format for testing.
func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	keyBytes, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyBytes,
	}))
}
