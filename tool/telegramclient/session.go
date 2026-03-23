package telegramclient

import (
	"context"
	"encoding/base64"
	"fmt"

	"tclaw/libraries/secret"
)

// secretSessionStorage adapts tclaw's secret.Store to gotd/td's session.Storage
// interface. The MTProto session bytes are base64-encoded and stored as a string
// in the encrypted secret store.
type secretSessionStorage struct {
	store secret.Store
	key   string
}

func newSecretSessionStorage(store secret.Store) *secretSessionStorage {
	return &secretSessionStorage{
		store: store,
		key:   SessionStoreKey,
	}
}

// LoadSession reads the persisted MTProto session from the secret store.
// Returns empty bytes (not an error) when no session exists — gotd treats
// this as "no prior session, start fresh."
func (s *secretSessionStorage) LoadSession(_ context.Context) ([]byte, error) {
	ctx := context.Background()
	encoded, err := s.store.Get(ctx, s.key)
	if err != nil {
		return nil, fmt.Errorf("read session from store: %w", err)
	}
	if encoded == "" {
		return nil, nil
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}
	return data, nil
}

// StoreSession persists the MTProto session to the secret store.
func (s *secretSessionStorage) StoreSession(_ context.Context, data []byte) error {
	ctx := context.Background()
	encoded := base64.StdEncoding.EncodeToString(data)
	if err := s.store.Set(ctx, s.key, encoded); err != nil {
		return fmt.Errorf("write session to store: %w", err)
	}
	return nil
}
