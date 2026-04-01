// Package store provides a key-value Store interface with a filesystem-backed implementation.
// JSON values are serialized to individual files on disk. Used for persisting agent state,
// session IDs, schedule data, and other structured data that needs to survive restarts.
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
