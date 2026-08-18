package garmin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileTokenStore persists a Token as JSON on disk. The field names match the file written by the
// python-garminconnect client, so a token bootstrapped there can be used here unchanged.
type FileTokenStore struct {
	path string
	mu   sync.Mutex
}

// NewFileTokenStore returns a store backed by the file at path.
func NewFileTokenStore(path string) *FileTokenStore {
	return &FileTokenStore{path: path}
}

// Load reads the stored token. A missing file yields ErrNoToken rather than an opaque OS error, so
// callers can tell "never logged in" from "something is broken".
func (s *FileTokenStore) Load(context.Context) (Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	switch {
	case os.IsNotExist(err):
		return Token{}, ErrNoToken
	case err != nil:
		return Token{}, fmt.Errorf("read token file %s: %w", s.path, err)
	}

	var token Token
	if err := json.Unmarshal(raw, &token); err != nil {
		return Token{}, fmt.Errorf("parse token file %s: %w", s.path, err)
	}
	return token, nil
}

// Save writes the token atomically, so a crash mid-write cannot leave a truncated file that would
// force a fresh MFA login.
func (s *FileTokenStore) Save(_ context.Context, token Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}
	encoded, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("encode token: %w", err)
	}

	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return fmt.Errorf("replace token file: %w", err)
	}
	return nil
}

// MemoryTokenStore keeps a token in memory. Intended for tests and for hosts that persist the
// token themselves (tclaw keeps it in its encrypted store rather than on disk).
type MemoryTokenStore struct {
	mu    sync.Mutex
	token Token
}

// NewMemoryTokenStore returns a store seeded with token.
func NewMemoryTokenStore(token Token) *MemoryTokenStore {
	return &MemoryTokenStore{token: token}
}

func (s *MemoryTokenStore) Load(context.Context) (Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token.RefreshToken == "" && s.token.AccessToken == "" {
		return Token{}, ErrNoToken
	}
	return s.token, nil
}

func (s *MemoryTokenStore) Save(_ context.Context, token Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
	return nil
}
