package remotemcp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"tclaw/connection"
	"tclaw/mcp/discovery"
)

// pendingRemoteMCPFlow handles the OAuth callback for remote MCP servers.
// It exchanges the authorization code using PKCE, stores the tokens, and
// triggers the config updater so the remote MCP's tools become available.
type pendingRemoteMCPFlow struct {
	name          string
	mcpURL        string
	authMeta      *discovery.AuthMetadata
	clientReg     *discovery.ClientRegistration
	manager       *connection.Manager
	configUpdater func(ctx context.Context) error
	codeVerifier  string
	done          chan struct{}
	result        string
	err           error
}

func (f *pendingRemoteMCPFlow) Complete(ctx context.Context, code string, callbackURL string) error {
	// Exchange the authorization code for tokens using PKCE.
	creds, err := discovery.ExchangeCodeWithPKCE(ctx, f.authMeta, f.clientReg, code, f.codeVerifier, callbackURL, f.mcpURL)
	if err != nil {
		return fmt.Errorf("code exchange failed: %w", err)
	}

	// Load existing auth data (has client registration info) and add tokens.
	auth, err := f.manager.GetRemoteMCPAuth(ctx, f.name)
	if err != nil {
		return fmt.Errorf("load existing auth: %w", err)
	}
	if auth == nil {
		return fmt.Errorf("no stored auth metadata for remote MCP %q", f.name)
	}

	auth.AccessToken = creds.AccessToken
	auth.RefreshToken = creds.RefreshToken
	if creds.ExpiresIn > 0 {
		auth.TokenExpiry = time.Now().Add(time.Duration(creds.ExpiresIn) * time.Second)
	}

	if err := f.manager.SetRemoteMCPAuth(ctx, f.name, auth); err != nil {
		return fmt.Errorf("store tokens: %w", err)
	}

	// Regenerate MCP config so the remote server's tools become available.
	if err := f.configUpdater(ctx); err != nil {
		slog.Error("failed to update mcp config after remote MCP auth", "name", f.name, "err", err)
	}

	f.result = fmt.Sprintf("Remote MCP %q authorized successfully", f.name)
	close(f.done)
	return nil
}

func (f *pendingRemoteMCPFlow) Fail(err error) {
	f.err = err
	close(f.done)
}

func (f *pendingRemoteMCPFlow) DoneChan() <-chan struct{} {
	return f.done
}
