package secretform_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/secretform"
)

func TestSecretFormDelete(t *testing.T) {
	t.Run("asks the user rather than deleting", func(t *testing.T) {
		h, armed := setupDelete(t, nil)

		result := callTool(t, h, "secret_form_delete", map[string]any{
			"key":    "old_api_token",
			"reason": "replaced by the new one",
		})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "awaiting_confirmation", got["status"])
		require.Equal(t, "old_api_token", got["key"])
		require.Len(t, *armed, 1)
		require.Equal(t, "old_api_token", (*armed)[0].Key)
		require.Equal(t, "replaced by the new one", (*armed)[0].Reason)
	})

	t.Run("refuses a key that could reach a namespaced credential", func(t *testing.T) {
		h, armed := setupDelete(t, nil)

		for _, key := range []string{"cred/git/default/token", "remote_mcp/browser", "channel/admin/token"} {
			err := callToolExpectError(t, h, "secret_form_delete", map[string]any{
				"key": key, "reason": "tidying",
			})
			require.Contains(t, err.Error(), "invalid characters", "key %q must be refused", key)
		}
		require.Empty(t, *armed, "nothing may be put to the user for an unreachable key")
	})

	t.Run("refuses a reserved key", func(t *testing.T) {
		h, armed := setupDelete(t, nil)

		err := callToolExpectError(t, h, "secret_form_delete", map[string]any{
			"key": "anthropic_api_key", "reason": "tidying",
		})
		require.Contains(t, err.Error(), "reserved")
		require.Empty(t, *armed)
	})

	t.Run("refuses a key the user never supplied", func(t *testing.T) {
		h, armed := setupDelete(t, nil)

		// The Telegram session is full account access and tclaw wrote it, not
		// the user, so it was never recorded as collected.
		err := callToolExpectError(t, h, "secret_form_delete", map[string]any{
			"key": "telegram_client_session", "reason": "tidying",
		})
		require.Contains(t, err.Error(), "not collected from you through a secret form")
		require.Empty(t, *armed)
	})

	t.Run("refuses everything when there is no record of what you supplied", func(t *testing.T) {
		handler := mcp.NewHandler()
		secretform.RegisterTools(handler, secretform.Deps{
			SecretStore:     newMemorySecretStore(),
			ArmSecretDelete: func(context.Context, secretform.DeleteRequest) error { return nil },
		})

		err := callToolExpectError(t, handler, "secret_form_delete", map[string]any{
			"key": "old_api_token", "reason": "tidying",
		})
		require.Contains(t, err.Error(), "none can be deleted",
			"a gate that knows nothing must refuse, not wave things through")
	})

	t.Run("requires a reason, because that is what the user decides on", func(t *testing.T) {
		h, _ := setupDelete(t, nil)

		err := callToolExpectError(t, h, "secret_form_delete", map[string]any{
			"key": "old_api_token", "reason": "   ",
		})
		require.Contains(t, err.Error(), "reason is required")
	})

	t.Run("fails when nobody can be asked", func(t *testing.T) {
		handler := mcp.NewHandler()
		stateStore := mustStateStore(t)
		recordCollected(t, stateStore, "old_api_token")
		secretform.RegisterTools(handler, secretform.Deps{
			SecretStore: newMemorySecretStore(),
			StateStore:  stateStore,
		})

		err := callToolExpectError(t, handler, "secret_form_delete", map[string]any{
			"key": "old_api_token", "reason": "tidying",
		})
		require.Contains(t, err.Error(), "no channel can be asked to confirm a deletion")
	})

	t.Run("surfaces why the confirmation could not be sent", func(t *testing.T) {
		h, _ := setupDelete(t, fmt.Errorf("no secret is stored under \"ghost\""))

		err := callToolExpectError(t, h, "secret_form_delete", map[string]any{
			"key": "ghost", "reason": "tidying",
		})
		require.Contains(t, err.Error(), "no secret is stored")
	})
}

func TestValidateDeletableKey(t *testing.T) {
	collected := map[string]bool{"old_api_token": true, "doc_wifi_password": true}
	tests := []struct {
		name    string
		key     string
		wantErr string
	}{
		{name: "a key the user supplied", key: "old_api_token"},
		{name: "a document key the user supplied", key: "doc_wifi_password"},
		{name: "empty", key: "", wantErr: "required"},
		{name: "slash", key: "cred/git/default/token", wantErr: "invalid characters"},
		{name: "uppercase", key: "OldApiToken", wantErr: "invalid characters"},
		{name: "reserved", key: "claude_setup_token", wantErr: "reserved"},
		{name: "written by a package, not the user", key: "telegram_client_session", wantErr: "not collected from you"},
		{name: "a key nobody has heard of", key: "made_up_key", wantErr: "not collected from you"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := secretform.ValidateDeletableKey(tt.key, collected)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}

	t.Run("an unknown allowlist allows nothing", func(t *testing.T) {
		err := secretform.ValidateDeletableKey("old_api_token", nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not known here",
			"nil must refuse, so neither call site can become a no-op gate")
	})
}

// --- helpers ---

// setupDelete registers the tools with an armer that records what it was asked
// to confirm, and fails with armErr when that is non-nil.
// setupDelete registers the tools with one key already recorded as user-supplied
// ("old_api_token"), so the allowlist has both a member and non-members.
func setupDelete(t *testing.T, armErr error) (*mcp.Handler, *[]secretform.DeleteRequest) {
	t.Helper()
	var armed []secretform.DeleteRequest
	handler := mcp.NewHandler()
	stateStore := mustStateStore(t)
	recordCollected(t, stateStore, "old_api_token", "ghost")

	secretform.RegisterTools(handler, secretform.Deps{
		SecretStore: newMemorySecretStore(),
		StateStore:  stateStore,
		ArmSecretDelete: func(_ context.Context, request secretform.DeleteRequest) error {
			if armErr != nil {
				return armErr
			}
			armed = append(armed, request)
			return nil
		},
	})
	return handler, &armed
}

func mustStateStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return s
}

// recordCollected writes the allowlist directly, standing in for the form
// submissions that would otherwise have populated it.
func recordCollected(t *testing.T, s store.Store, keys ...string) {
	t.Helper()
	data, err := json.Marshal(keys)
	require.NoError(t, err)
	require.NoError(t, s.Set(context.Background(), "agent_collected_secret_keys", data))
}
