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
	path := filepath.Join(f.dir, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create dir for %s: %w", key, err)
	}
	if err := os.WriteFile(path, value, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", key, err)
	}
	return nil
}

func (f *FS) Delete(_ context.Context, key string) error {
	err := os.Remove(filepath.Join(f.dir, key))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}
