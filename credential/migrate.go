package credential

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tclaw/libraries/secret"
	"tclaw/libraries/store"
)

// legacyConnection mirrors connection.Connection for migration without
// importing the connection package.
type legacyConnection struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	Label      string `json:"label"`
	Channel    string `json:"channel"`
}

// legacyCredentials mirrors connection.Credentials for migration.
type legacyCredentials struct {
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	ExpiresAt    string            `json:"expires_at,omitempty"` // read as string, parse in OAuthTokens
	Extra        map[string]string `json:"extra,omitempty"`
}

// MigrateFromConnections is a one-time migration that reads legacy connections
// from the state store and copies their OAuth tokens into credential sets. This
// allows the old connection/provider system to be removed.
//
// It's idempotent — if a credential set already exists with OAuth tokens, the
// connection is skipped. Run this at startup before registerCredentialSystem.
//
// configSecrets maps package name → field name → value, providing config-level
// secrets (e.g. client_id/client_secret) to seed alongside the migrated tokens.
func MigrateFromConnections(ctx context.Context, stateStore store.Store, secretStore secret.Store, credMgr *Manager, configSecrets map[string]map[string]string) error {
	// Read legacy connections.
	data, err := stateStore.Get(ctx, "connections")
	if err != nil {
		return fmt.Errorf("read legacy connections: %w", err)
	}
	if len(data) == 0 {
		return nil
	}

	var conns []legacyConnection
	if err := json.Unmarshal(data, &conns); err != nil {
		return fmt.Errorf("parse legacy connections: %w", err)
	}

	if len(conns) == 0 {
		return nil
	}

	slog.Info("migration: found legacy connections", "count", len(conns))

	migrated := 0
	skipped := 0
	for _, conn := range conns {
		setID := NewCredentialSetID(conn.ProviderID, conn.Label)

		// Skip if credential set already exists with tokens.
		existing, err := credMgr.Get(ctx, setID)
		if err != nil {
			slog.Warn("migration: failed to check credential set", "set", setID, "err", err)
			continue
		}
		if existing != nil {
			tokens, _ := credMgr.GetOAuthTokens(ctx, setID)
			if tokens != nil && tokens.AccessToken != "" {
				skipped++
				slog.Debug("migration: already migrated, skipping", "connection", conn.ID)
				continue
			}
		}

		// Read legacy credentials from secret store.
		credKey := "conn/" + conn.ID
		credJSON, err := secretStore.Get(ctx, credKey)
		if err != nil {
			slog.Warn("migration: failed to read legacy credentials", "connection", conn.ID, "err", err)
			continue
		}
		if credJSON == "" {
			// No credentials stored — skip (unauthenticated connection).
			continue
		}

		var legacyCreds legacyCredentials
		if err := json.Unmarshal([]byte(credJSON), &legacyCreds); err != nil {
			slog.Warn("migration: failed to parse legacy credentials", "connection", conn.ID, "err", err)
			continue
		}

		if legacyCreds.AccessToken == "" {
			continue
		}

		// Create credential set if it doesn't exist.
		if existing == nil {
			if _, err := credMgr.Add(ctx, conn.ProviderID, conn.Label, conn.Channel); err != nil {
				slog.Warn("migration: failed to create credential set", "connection", conn.ID, "err", err)
				continue
			}
		}

		// Seed config-level secrets (e.g. client_id, client_secret).
		if secrets, ok := configSecrets[conn.ProviderID]; ok {
			for key, val := range secrets {
				if val != "" {
					if err := credMgr.SetField(ctx, setID, key, val); err != nil {
						slog.Warn("migration: failed to set field from config", "set", setID, "field", key, "err", err)
					}
				}
			}
		}

		// Copy OAuth tokens. Parse ExpiresAt from the raw JSON to preserve the
		// original time — we read it as a raw credential blob rather than parsing
		// into time.Time (which may lose precision with different formats).
		tokens := &OAuthTokens{
			AccessToken:  legacyCreds.AccessToken,
			RefreshToken: legacyCreds.RefreshToken,
		}
		// Re-parse the full credentials JSON to get ExpiresAt as time.Time.
		var fullCreds struct {
			ExpiresAt jsonTime `json:"expires_at"`
		}
		if err := json.Unmarshal([]byte(credJSON), &fullCreds); err == nil {
			tokens.ExpiresAt = fullCreds.ExpiresAt.Time
		}

		if err := credMgr.SetOAuthTokens(ctx, setID, tokens); err != nil {
			slog.Warn("migration: failed to copy oauth tokens", "set", setID, "err", err)
			continue
		}

		migrated++
		slog.Info("migrated connection to credential set", "connection", conn.ID, "credential_set", setID)
	}

	slog.Info("connection migration complete", "migrated", migrated, "skipped", skipped, "total", len(conns))

	return nil
}
