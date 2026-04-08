package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FS is a filesystem-backed store. Each key becomes a file inside dir.
type FS struct {
	dir string
}

func NewFS(dir string) (*FS, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	return &FS{dir: dir}, nil
}

// safePath resolves a key to a file path within the store directory,
// returning an error if the key would escape via path traversal.
func (f *FS) safePath(key string) (string, error) {
	path := filepath.Join(f.dir, key)
	cleaned := filepath.Clean(path)
	cleanedDir := filepath.Clean(f.dir)
	if !strings.HasPrefix(cleaned, cleanedDir+string(filepath.Separator)) && cleaned != cleanedDir {
		return "", fmt.Errorf("invalid key %q: path traversal detected", key)
	}
	return cleaned, nil
}

func (f *FS) Get(_ context.Context, key string) ([]byte, error) {
	path, err := f.safePath(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	return data, nil
}

func (f *FS) Set(_ context.Context, key string, value []byte) error {
	path, err := f.safePath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create dir for %s: %w", key, err)
	}
	if err := os.WriteFile(path, value, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", key, err)
	}
	return nil
}

func (f *FS) Delete(_ context.Context, key string) error {
	path, err := f.safePath(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}
