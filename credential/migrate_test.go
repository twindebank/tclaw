package credential_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/credential"
	"tclaw/libraries/store"
)

func TestMigrateFromConnections(t *testing.T) {
	t.Run("migrates connection with oauth tokens", func(t *testing.T) {
		s, sec := setupMigration(t)
		credMgr := credential.NewManager(s, sec)
		ctx := context.Background()

		// Seed a legacy connection.
		conns := []map[string]string{{
			"id":          "google/work",
			"provider_id": "google",
			"label":       "work",
			"channel":     "admin",
		}}
		connsJSON, err := json.Marshal(conns)
		require.NoError(t, err)
		require.NoError(t, s.Set(ctx, "connections", connsJSON))

		// Seed legacy credentials.
		creds := map[string]any{
			"access_token":  "access-123",
			"refresh_token": "refresh-456",
			"expires_at":    time.Now().Add(time.Hour).Format(time.RFC3339),
		}
		credsJSON, err := json.Marshal(creds)
		require.NoError(t, err)
		require.NoError(t, sec.Set(ctx, "conn/google/work", string(credsJSON)))

		// Run migration.
		clients := map[string]credential.OAuthClientCredentials{
			"google": {ClientID: "goog-id", ClientSecret: "goog-secret"},
		}
		err = credential.MigrateFromConnections(ctx, s, sec, credMgr, clients)
		require.NoError(t, err)

		// Verify credential set was created.
		set, err := credMgr.Get(ctx, "google/work")
		require.NoError(t, err)
		require.NotNil(t, set)
		require.Equal(t, "google", set.Package)
		require.Equal(t, "work", set.Label)
		require.Equal(t, "admin", set.Channel)

		// Verify OAuth tokens were copied.
		tokens, err := credMgr.GetOAuthTokens(ctx, "google/work")
		require.NoError(t, err)
		require.Equal(t, "access-123", tokens.AccessToken)
		require.Equal(t, "refresh-456", tokens.RefreshToken)

		// Verify client credentials were seeded.
		clientID, err := credMgr.GetField(ctx, "google/work", "client_id")
		require.NoError(t, err)
		require.Equal(t, "goog-id", clientID)
	})

	t.Run("skips already migrated connections", func(t *testing.T) {
		s, sec := setupMigration(t)
		credMgr := credential.NewManager(s, sec)
		ctx := context.Background()

		// Seed a legacy connection.
		conns := []map[string]string{{
			"id":          "google/work",
			"provider_id": "google",
			"label":       "work",
			"channel":     "",
		}}
		connsJSON, err := json.Marshal(conns)
		require.NoError(t, err)
		require.NoError(t, s.Set(ctx, "connections", connsJSON))

		credsJSON, _ := json.Marshal(map[string]any{"access_token": "old-token"})
		require.NoError(t, sec.Set(ctx, "conn/google/work", string(credsJSON)))

		// Pre-create the credential set with tokens.
		_, err = credMgr.Add(ctx, "google", "work", "")
		require.NoError(t, err)
		require.NoError(t, credMgr.SetOAuthTokens(ctx, "google/work", &credential.OAuthTokens{
			AccessToken: "new-token",
		}))

		// Run migration — should skip.
		err = credential.MigrateFromConnections(ctx, s, sec, credMgr, nil)
		require.NoError(t, err)

		// Verify the existing token was NOT overwritten.
		tokens, err := credMgr.GetOAuthTokens(ctx, "google/work")
		require.NoError(t, err)
		require.Equal(t, "new-token", tokens.AccessToken)
	})

	t.Run("handles empty connections gracefully", func(t *testing.T) {
		s, sec := setupMigration(t)
		credMgr := credential.NewManager(s, sec)
		ctx := context.Background()

		err := credential.MigrateFromConnections(ctx, s, sec, credMgr, nil)
		require.NoError(t, err)
	})

	t.Run("skips connections without credentials", func(t *testing.T) {
		s, sec := setupMigration(t)
		credMgr := credential.NewManager(s, sec)
		ctx := context.Background()

		conns := []map[string]string{{
			"id":          "monzo/personal",
			"provider_id": "monzo",
			"label":       "personal",
			"channel":     "",
		}}
		connsJSON, _ := json.Marshal(conns)
		require.NoError(t, s.Set(ctx, "connections", connsJSON))

		// No credentials seeded — should skip without error.
		err := credential.MigrateFromConnections(ctx, s, sec, credMgr, nil)
		require.NoError(t, err)

		// Verify no credential set was created.
		set, err := credMgr.Get(ctx, "monzo/personal")
		require.NoError(t, err)
		require.Nil(t, set)
	})
}

// --- helpers ---

func setupMigration(t *testing.T) (store.Store, *memorySecretStore) {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return s, &memorySecretStore{data: make(map[string]string)}
}
