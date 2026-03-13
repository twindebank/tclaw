package secret

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/secretbox"
)

const (
	keySize   = 32 // NaCl secretbox key size
	nonceSize = 24 // NaCl secretbox nonce size
	hkdfInfo  = "tclaw-secret-store"
)

// EncryptedFSStore encrypts secrets at rest using NaCl secretbox
// (XSalsa20-Poly1305). Each user gets a unique encryption key derived
// via HKDF from a master key + user ID, so compromising one user's
// files doesn't expose another's secrets.
type EncryptedFSStore struct {
	dir string
	key [keySize]byte
}

// NewEncryptedFSStore creates an encrypted file-backed store.
// masterKey is the shared secret (from TCLAW_SECRET_KEY env var).
// userID derives a per-user encryption key via HKDF.
func NewEncryptedFSStore(dir string, masterKey []byte, userID string) (*EncryptedFSStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create secret store dir: %w", err)
	}

	// Derive a per-user key: HKDF-SHA256(master, salt=userID, info="tclaw-secret-store")
	hkdfReader := hkdf.New(sha256.New, masterKey, []byte(userID), []byte(hkdfInfo))
	var key [keySize]byte
	if _, err := io.ReadFull(hkdfReader, key[:]); err != nil {
		return nil, fmt.Errorf("derive encryption key: %w", err)
	}

	return &EncryptedFSStore{dir: dir, key: key}, nil
}

// safePath resolves a key to a file path within the store directory,
// returning an error if the key would escape via path traversal.
func (e *EncryptedFSStore) safePath(key string) (string, error) {
	path := filepath.Join(e.dir, key)
	cleaned := filepath.Clean(path)
	cleanedDir := filepath.Clean(e.dir)
	if !strings.HasPrefix(cleaned, cleanedDir+string(filepath.Separator)) && cleaned != cleanedDir {
		return "", fmt.Errorf("invalid key %q: path traversal detected", key)
	}
	return cleaned, nil
}

func (e *EncryptedFSStore) Get(_ context.Context, key string) (string, error) {
	path, err := e.safePath(key)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read secret %q: %w", key, err)
	}

	if len(data) < nonceSize {
		return "", fmt.Errorf("secret %q: corrupt data (too short)", key)
	}

	var nonce [nonceSize]byte
	copy(nonce[:], data[:nonceSize])

	plaintext, ok := secretbox.Open(nil, data[nonceSize:], &nonce, &e.key)
	if !ok {
		return "", fmt.Errorf("secret %q: decryption failed (wrong key or corrupt data)", key)
	}

	return string(plaintext), nil
}

func (e *EncryptedFSStore) Set(_ context.Context, key string, value string) error {
	path, err := e.safePath(key)
	if err != nil {
		return err
	}

	var nonce [nonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}

	// nonce + encrypted data
	sealed := secretbox.Seal(nonce[:], []byte(value), &nonce, &e.key)

	// Keys like "conn/google/personal" contain slashes, so ensure
	// intermediate directories exist before writing.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create secret dir for %q: %w", key, err)
	}

	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		return fmt.Errorf("write secret %q: %w", key, err)
	}
	return nil
}

func (e *EncryptedFSStore) Delete(_ context.Context, key string) error {
	path, err := e.safePath(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete secret %q: %w", key, err)
	}
	return nil
}
