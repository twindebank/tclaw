package secret

import (
	"context"
	"fmt"

	"github.com/zalando/go-keyring"
)

const (
	keychainServicePrefix = "tclaw/internal/"
	keychainProbeService  = "tclaw/internal/_probe"
	keychainProbeKey      = "_availability_check"
)

// KeychainStore uses the OS credential store (macOS Keychain, Linux
// Secret Service, Windows Credential Manager). Each user gets an
// isolated service namespace so secrets can't leak across users.
type KeychainStore struct {
	service string // keychain service name, scoped per user
}

// NewKeychainStore creates a store backed by the OS keychain.
// userID is used to namespace secrets so each user is isolated.
func NewKeychainStore(userID string) *KeychainStore {
	return &KeychainStore{service: keychainServicePrefix + userID}
}

func (k *KeychainStore) Get(_ context.Context, key string) (string, error) {
	val, err := keyring.Get(k.service, key)
	if err == keyring.ErrNotFound {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("keychain get %q: %w", key, err)
	}
	return val, nil
}

func (k *KeychainStore) Set(_ context.Context, key string, value string) error {
	if err := keyring.Set(k.service, key, value); err != nil {
		return fmt.Errorf("keychain set %q: %w", key, err)
	}
	return nil
}

func (k *KeychainStore) Delete(_ context.Context, key string) error {
	if err := keyring.Delete(k.service, key); err != nil && err != keyring.ErrNotFound {
		return fmt.Errorf("keychain delete %q: %w", key, err)
	}
	return nil
}

// KeychainAvailable reports whether the OS keychain is usable.
// Returns false in containers or headless environments.
func KeychainAvailable() bool {
	if err := keyring.Set(keychainProbeService, keychainProbeKey, "ok"); err != nil {
		return false
	}
	_ = keyring.Delete(keychainProbeService, keychainProbeKey)
	return true
}
