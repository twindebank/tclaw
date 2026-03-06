package secret

import "context"

// Store provides secure persistent storage for secrets (OAuth tokens,
// API keys, etc). Implementations must encrypt at rest.
type Store interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string) error
	Delete(ctx context.Context, key string) error
}
