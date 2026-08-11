package router

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/config"
	"tclaw/internal/credential"
	"tclaw/internal/libraries/store"
)

func TestSeedCredentialSlots(t *testing.T) {
	t.Run("creates a set and writes the fields config supplies", func(t *testing.T) {
		manager, secrets := newCredentialManager(t)

		require.NoError(t, seedCredentialSlots(context.Background(), manager, []config.CredentialSlot{{
			Type:   "git",
			Label:  "default",
			Fields: map[string]string{"token": "ghp_from_config"},
		}}))

		set, err := manager.Get(context.Background(), credential.NewCredentialSetID("git", "default"))
		require.NoError(t, err)
		require.NotNil(t, set)

		value, err := secrets.Get(context.Background(), "cred/git/default/token")
		require.NoError(t, err)
		require.Equal(t, "ghp_from_config", value)
	})

	t.Run("creates an empty set for a slot with no fields", func(t *testing.T) {
		// A declared-but-unset slot is what makes "declare now, fill from a
		// phone later" possible, so the set must exist to be referenced.
		manager, _ := newCredentialManager(t)

		require.NoError(t, seedCredentialSlots(context.Background(), manager, []config.CredentialSlot{{
			Type:  "git",
			Label: "homeassistant",
		}}))

		set, err := manager.Get(context.Background(), credential.NewCredentialSetID("git", "homeassistant"))
		require.NoError(t, err)
		require.NotNil(t, set)

		token, err := manager.GetField(context.Background(), set.ID, "token")
		require.NoError(t, err)
		require.Empty(t, token)
	})

	t.Run("leaves a stored value alone when config supplies an empty one", func(t *testing.T) {
		// A boot secret that has gone missing resolves to empty; wiping a
		// credential the user filled in by hand would be the worst outcome.
		manager, _ := newCredentialManager(t)
		setID := credential.NewCredentialSetID("git", "default")

		require.NoError(t, seedCredentialSlots(context.Background(), manager, []config.CredentialSlot{{
			Type: "git", Label: "default", Fields: map[string]string{"token": "filled_by_hand"},
		}}))
		require.NoError(t, seedCredentialSlots(context.Background(), manager, []config.CredentialSlot{{
			Type: "git", Label: "default", Fields: map[string]string{"token": ""},
		}}))

		token, err := manager.GetField(context.Background(), setID, "token")
		require.NoError(t, err)
		require.Equal(t, "filled_by_hand", token)
	})

	t.Run("is idempotent across boots", func(t *testing.T) {
		manager, _ := newCredentialManager(t)
		slots := []config.CredentialSlot{{Type: "git", Label: "default", Fields: map[string]string{"token": "t"}}}

		require.NoError(t, seedCredentialSlots(context.Background(), manager, slots))
		require.NoError(t, seedCredentialSlots(context.Background(), manager, slots))

		sets, err := manager.List(context.Background())
		require.NoError(t, err)
		require.Len(t, sets, 1)
	})

	t.Run("defaults an empty label", func(t *testing.T) {
		manager, _ := newCredentialManager(t)

		require.NoError(t, seedCredentialSlots(context.Background(), manager, []config.CredentialSlot{{Type: "git"}}))

		set, err := manager.Get(context.Background(), credential.NewCredentialSetID("git", "default"))
		require.NoError(t, err)
		require.NotNil(t, set)
	})
}

// --- helpers ---

func newCredentialManager(t *testing.T) (*credential.Manager, *memorySecretStore) {
	t.Helper()
	s, err := store.NewFS(filepath.Join(t.TempDir(), "state"))
	require.NoError(t, err)
	secrets := &memorySecretStore{data: map[string]string{}}
	return credential.NewManager(s, secrets), secrets
}
