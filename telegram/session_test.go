package telegram

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionStorage(t *testing.T) {
	t.Run("round-trip store and load", func(t *testing.T) {
		secrets := &memorySecretStore{data: map[string]string{}}
		storage := newSecretSessionStorage(secrets, "test_session_key")

		original := []byte("test session data with binary \x00\x01\x02\xff")

		err := storage.StoreSession(context.Background(), original)
		require.NoError(t, err)

		// Verify it's stored as base64 in the secret store.
		raw := secrets.data["test_session_key"]
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
		storage := newSecretSessionStorage(secrets, "test_session_key")

		loaded, err := storage.LoadSession(context.Background())
		require.NoError(t, err)
		require.Nil(t, loaded)
	})

	t.Run("load rejects corrupt base64", func(t *testing.T) {
		secrets := &memorySecretStore{data: map[string]string{
			"test_session_key": "not-valid-base64!!!",
		}}
		storage := newSecretSessionStorage(secrets, "test_session_key")

		_, err := storage.LoadSession(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "decode session")
	})
}

// --- helpers ---

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
