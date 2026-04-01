package credential

import (
	"context"
	"encoding/json"
	"fmt"
)

// SetOAuthTokens stores OAuth tokens for a credential set.
func (m *Manager) SetOAuthTokens(ctx context.Context, id CredentialSetID, tokens *OAuthTokens) error {
	data, err := json.Marshal(tokens)
	if err != nil {
		return fmt.Errorf("marshal oauth tokens: %w", err)
	}
	if err := m.secrets.Set(ctx, fieldKey(id, oauthFieldKey), string(data)); err != nil {
		return fmt.Errorf("store oauth tokens for %s: %w", id, err)
	}
	return nil
}

// GetOAuthTokens retrieves OAuth tokens for a credential set. Returns nil if
// no tokens are stored.
func (m *Manager) GetOAuthTokens(ctx context.Context, id CredentialSetID) (*OAuthTokens, error) {
	val, err := m.secrets.Get(ctx, fieldKey(id, oauthFieldKey))
	if err != nil {
		return nil, fmt.Errorf("read oauth tokens for %s: %w", id, err)
	}
	if val == "" {
		return nil, nil
	}
	var tokens OAuthTokens
	if err := json.Unmarshal([]byte(val), &tokens); err != nil {
		return nil, fmt.Errorf("parse oauth tokens for %s: %w", id, err)
	}
	return &tokens, nil
}

// RefreshIfNeeded returns valid OAuth tokens, refreshing them if expired.
// Returns an error if no tokens exist or refresh fails.
func (m *Manager) RefreshIfNeeded(ctx context.Context, id CredentialSetID, refreshFn RefreshFunc) (*OAuthTokens, error) {
	tokens, err := m.GetOAuthTokens(ctx, id)
	if err != nil {
		return nil, err
	}
	if tokens == nil {
		return nil, fmt.Errorf("no oauth tokens for credential set %q", id)
	}

	if !tokens.Expired() {
		return tokens, nil
	}

	if tokens.RefreshToken == "" {
		return nil, fmt.Errorf("oauth tokens for %q expired and no refresh token available", id)
	}

	newTokens, err := refreshFn(ctx, tokens.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh oauth tokens: %w", err)
	}

	// Preserve the refresh token if the new response doesn't include one.
	if newTokens.RefreshToken == "" {
		newTokens.RefreshToken = tokens.RefreshToken
	}

	if err := m.SetOAuthTokens(ctx, id, newTokens); err != nil {
		return nil, err
	}
	return newTokens, nil
}
