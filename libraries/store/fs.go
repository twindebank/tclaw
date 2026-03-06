package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

func (f *FS) Get(_ context.Context, key string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(f.dir, key))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	return data, nil
}

func (f *FS) Set(_ context.Context, key string, value []byte) error {
	if err := os.WriteFile(filepath.Join(f.dir, key), value, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", key, err)
	}
	return nil
}
