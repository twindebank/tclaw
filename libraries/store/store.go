package store

import "context"

// Store is a simple key-value store for agent state (session IDs, etc.).
// The filesystem implementation works locally; swap in a cloud-backed
// implementation when deploying to containers.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
}
