package restauranttools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/mcp"
	"tclaw/tool/restauranttools"
)

func TestSetCredentials(t *testing.T) {
	t.Run("stores resy credentials", func(t *testing.T) {
		h, secrets := setup(t)

		result := callTool(t, h, "restaurant_set_credentials", map[string]any{
			"provider":   "resy",
			"api_key":    "test-api-key",
			"auth_token": "test-auth-token",
		})

		var got map[string]string
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "ok", got["status"])
		require.Equal(t, "resy", got["provider"])

		require.Equal(t, "test-api-key", secrets.data[restauranttools.ResyAPIKeyStoreKey])
		require.Equal(t, "test-auth-token", secrets.data[restauranttools.ResyAuthTokenStoreKey])
	})

	t.Run("defaults to resy provider", func(t *testing.T) {
		h, secrets := setup(t)

		result := callTool(t, h, "restaurant_set_credentials", map[string]any{
			"api_key":    "my-key",
			"auth_token": "my-token",
		})

		var got map[string]string
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "resy", got["provider"])
		require.Equal(t, "my-key", secrets.data[restauranttools.ResyAPIKeyStoreKey])
	})

	t.Run("rejects unknown provider", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "restaurant_set_credentials", map[string]any{
			"provider":   "unknown",
			"api_key":    "key",
			"auth_token": "token",
		})
		require.Contains(t, err.Error(), "unknown restaurant provider")
	})

	t.Run("rejects missing api_key", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "restaurant_set_credentials", map[string]any{
			"provider":   "resy",
			"api_key":    "",
			"auth_token": "token",
		})
		require.Contains(t, err.Error(), "api_key")
	})

	t.Run("rejects missing auth_token", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "restaurant_set_credentials", map[string]any{
			"provider":   "resy",
			"api_key":    "key",
			"auth_token": "",
		})
		require.Contains(t, err.Error(), "auth_token")
	})
}

func TestSearch(t *testing.T) {
	t.Run("requires credentials", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "restaurant_search", map[string]any{
			"day": "2026-04-15",
		})
		require.Contains(t, err.Error(), "credentials not configured")
	})

	t.Run("requires day parameter", func(t *testing.T) {
		h, _ := setupWithCredentials(t)

		err := callToolExpectError(t, h, "restaurant_search", map[string]any{
			"query": "noma",
		})
		require.Contains(t, err.Error(), "day is required")
	})
}

func TestAvailability(t *testing.T) {
	t.Run("requires credentials", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "restaurant_availability", map[string]any{
			"venue_id": "12345",
			"day":      "2026-04-15",
		})
		require.Contains(t, err.Error(), "credentials not configured")
	})

	t.Run("requires venue_id", func(t *testing.T) {
		h, _ := setupWithCredentials(t)

		err := callToolExpectError(t, h, "restaurant_availability", map[string]any{
			"day": "2026-04-15",
		})
		require.Contains(t, err.Error(), "venue_id is required")
	})

	t.Run("requires day", func(t *testing.T) {
		h, _ := setupWithCredentials(t)

		err := callToolExpectError(t, h, "restaurant_availability", map[string]any{
			"venue_id": "12345",
		})
		require.Contains(t, err.Error(), "day is required")
	})
}

func TestBook(t *testing.T) {
	t.Run("requires credentials", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "restaurant_book", map[string]any{
			"config_id":         "cfg_123",
			"day":               "2026-04-15",
			"party_size":        2,
			"payment_method_id": 1,
		})
		require.Contains(t, err.Error(), "credentials not configured")
	})

	t.Run("requires config_id", func(t *testing.T) {
		h, _ := setupWithCredentials(t)

		err := callToolExpectError(t, h, "restaurant_book", map[string]any{
			"day":               "2026-04-15",
			"party_size":        2,
			"payment_method_id": 1,
		})
		require.Contains(t, err.Error(), "config_id is required")
	})

	t.Run("requires day", func(t *testing.T) {
		h, _ := setupWithCredentials(t)

		err := callToolExpectError(t, h, "restaurant_book", map[string]any{
			"config_id":         "cfg_123",
			"party_size":        2,
			"payment_method_id": 1,
		})
		require.Contains(t, err.Error(), "day is required")
	})

	t.Run("requires party_size", func(t *testing.T) {
		h, _ := setupWithCredentials(t)

		err := callToolExpectError(t, h, "restaurant_book", map[string]any{
			"config_id":         "cfg_123",
			"day":               "2026-04-15",
			"payment_method_id": 1,
		})
		require.Contains(t, err.Error(), "party_size is required")
	})

	t.Run("requires payment_method_id", func(t *testing.T) {
		h, _ := setupWithCredentials(t)

		err := callToolExpectError(t, h, "restaurant_book", map[string]any{
			"config_id":  "cfg_123",
			"day":        "2026-04-15",
			"party_size": 2,
		})
		require.Contains(t, err.Error(), "payment_method_id is required")
	})
}

func TestCancel(t *testing.T) {
	t.Run("requires reservation_id", func(t *testing.T) {
		h, _ := setupWithCredentials(t)

		err := callToolExpectError(t, h, "restaurant_cancel", map[string]any{})
		require.Contains(t, err.Error(), "reservation_id is required")
	})

	t.Run("returns not supported for resy", func(t *testing.T) {
		h, _ := setupWithCredentials(t)

		err := callToolExpectError(t, h, "restaurant_cancel", map[string]any{
			"reservation_id": "res_123",
		})
		require.Contains(t, err.Error(), "not supported")
	})
}

func TestListBookings(t *testing.T) {
	t.Run("returns not supported for resy", func(t *testing.T) {
		h, _ := setupWithCredentials(t)

		err := callToolExpectError(t, h, "restaurant_list_bookings", map[string]any{})
		require.Contains(t, err.Error(), "not supported")
	})
}

// --- helpers ---

func setup(t *testing.T) (*mcp.Handler, *memorySecretStore) {
	t.Helper()
	secrets := &memorySecretStore{data: map[string]string{}}
	handler := mcp.NewHandler()
	restauranttools.RegisterTools(handler, restauranttools.Deps{
		SecretStore: secrets,
	})
	return handler, secrets
}

func setupWithCredentials(t *testing.T) (*mcp.Handler, *memorySecretStore) {
	t.Helper()
	secrets := &memorySecretStore{data: map[string]string{
		restauranttools.ResyAPIKeyStoreKey:    "test-api-key",
		restauranttools.ResyAuthTokenStoreKey: "test-auth-token",
	}}
	handler := mcp.NewHandler()
	restauranttools.RegisterTools(handler, restauranttools.Deps{
		SecretStore: secrets,
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
