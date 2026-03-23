package telegramclient

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	"tclaw/libraries/secret"
)

// Client wraps a gotd/td telegram.Client with lazy initialization and session
// persistence through the encrypted secret store. The client connects on first
// use and stays alive for the user's lifetime.
type Client struct {
	mu      sync.Mutex
	client  *telegram.Client
	authCli *auth.Client
	api     *tg.Client
	ctx     context.Context
	cancel  context.CancelFunc
	ready   chan struct{}
	stopped chan struct{}
	apiID   int
	apiHash string
	storage *secretSessionStorage
}

// NewClient creates a Client that will lazily connect using the given credentials.
func NewClient(apiID int, apiHash string, secretStore secret.Store) *Client {
	return &Client{
		apiID:   apiID,
		apiHash: apiHash,
		storage: newSecretSessionStorage(secretStore),
		ready:   make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

// Connect starts the MTProto client in a background goroutine. The client
// persists its session via the secretSessionStorage adapter. Idempotent — calling
// Connect on an already-connected client is a no-op.
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		return nil
	}

	c.ctx, c.cancel = context.WithCancel(context.Background())

	c.client = telegram.NewClient(c.apiID, c.apiHash, telegram.Options{
		SessionStorage: c.storage,
	})

	go func() {
		defer close(c.stopped)
		err := c.client.Run(c.ctx, func(ctx context.Context) error {
			// Client is connected — capture the API handles.
			c.mu.Lock()
			c.api = c.client.API()
			c.authCli = c.client.Auth()
			c.mu.Unlock()
			close(c.ready)

			// Block until context is cancelled.
			<-ctx.Done()
			return ctx.Err()
		})
		if err != nil && c.ctx.Err() == nil {
			slog.Error("telegram client stopped unexpectedly", "err", err)
		}
	}()

	return nil
}

// WaitReady blocks until the client is connected and the API is available.
func (c *Client) WaitReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ready:
		return nil
	case <-c.stopped:
		return fmt.Errorf("telegram client stopped before becoming ready")
	}
}

// API returns the raw Telegram API client. Must only be called after WaitReady.
func (c *Client) API() *tg.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.api
}

// Auth returns the higher-level auth client. Must only be called after WaitReady.
func (c *Client) Auth() *auth.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authCli
}

// IsReady reports whether the client is connected and the API is available.
func (c *Client) IsReady() bool {
	select {
	case <-c.ready:
		return true
	default:
		return false
	}
}

// Close shuts down the MTProto client and waits for it to stop.
func (c *Client) Close() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
		<-c.stopped
	}

	c.mu.Lock()
	c.client = nil
	c.api = nil
	c.authCli = nil
	c.cancel = nil
	c.ready = make(chan struct{})
	c.stopped = make(chan struct{})
	c.mu.Unlock()
}
