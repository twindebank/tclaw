package garmintools_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/mcp"
	"tclaw/internal/tool/garmintools"
)

func TestGarminSettingsSearch(t *testing.T) {
	t.Run("finds settings by substring", func(t *testing.T) {
		handler, _ := setup(t)

		result := callTool(t, handler, garmintools.ToolSettingsSearch, map[string]any{
			"term": "backlight",
		})

		var got struct {
			Count    int `json:"count"`
			Settings []struct {
				Setting     string `json:"setting"`
				Type        string `json:"type"`
				PerActivity bool   `json:"per_activity"`
			} `json:"settings"`
		}
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, 1, got.Count)
		require.Equal(t, "DeviceSettingId.BACKLIGHT_MODE", got.Settings[0].Setting)
		require.Equal(t, "string", got.Settings[0].Type)
		require.False(t, got.Settings[0].PerActivity)
	})

	t.Run("marks per-activity settings", func(t *testing.T) {
		handler, _ := setup(t)

		result := callTool(t, handler, garmintools.ToolSettingsSearch, map[string]any{
			"term": "HR_ZONE1_FLOOR",
		})

		var got struct {
			Settings []struct {
				PerActivity bool `json:"per_activity"`
			} `json:"settings"`
		}
		require.NoError(t, json.Unmarshal(result, &got))
		require.Len(t, got.Settings, 1)
		require.True(t, got.Settings[0].PerActivity, "zone floors need a sport")
	})

	t.Run("works without credentials", func(t *testing.T) {
		// The catalogue is local data; requiring a token to browse it would be a poor experience.
		handler, store := setup(t)
		require.Empty(t, store.data, "precondition: nothing stored")

		result := callTool(t, handler, garmintools.ToolSettingsSearch, map[string]any{"term": "unit"})

		var got struct {
			Count int `json:"count"`
		}
		require.NoError(t, json.Unmarshal(result, &got))
		require.Positive(t, got.Count)
	})
}

func TestGarminSettingsSet_Validation(t *testing.T) {
	// These all fail before any network call, so they exercise the real validation path.
	t.Run("rejects a missing device id", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolSettingsSet, map[string]any{
			"setting": "TIME_FORMAT", "value": "time_twenty_four_hr",
		})

		require.Contains(t, err.Error(), "device_id is required")
	})

	t.Run("rejects an unknown setting", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolSettingsSet, map[string]any{
			"device_id": 123, "setting": "NOT_A_REAL_SETTING", "value": "x",
		})

		require.Contains(t, err.Error(), "unknown setting")
	})

	t.Run("rejects an ambiguous setting name", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolSettingsSet, map[string]any{
			"device_id": 123, "setting": "START_OF_WEEK", "value": "MONDAY",
		})

		require.Contains(t, err.Error(), "ambiguous")
	})

	t.Run("rejects a value of the wrong type", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolSettingsSet, map[string]any{
			"device_id": 123, "setting": "AUTO_SYNC_STEPS_BEFORE_SYNC", "value": "not-a-number",
		})

		require.Contains(t, err.Error(), "wants an integer")
	})

	t.Run("requires a sport for a per-activity setting", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolSettingsSet, map[string]any{
			"device_id": 123, "setting": "HR_ZONE1_FLOOR", "value": "95",
		})

		require.Contains(t, err.Error(), "per-activity")
	})

	t.Run("rejects a sport on a device-wide setting", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolSettingsSet, map[string]any{
			"device_id": 123, "setting": "TIME_FORMAT", "value": "time_twenty_four_hr",
			"sport": "running",
		})

		require.Contains(t, err.Error(), "not a per-activity setting")
	})

	t.Run("refuses to write a Garmin-derived setting", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolSettingsSet, map[string]any{
			"device_id": 123, "setting": "DerivedSettingId.AGE", "value": "40",
		})

		require.Contains(t, err.Error(), "derived by Garmin")
	})
}

func TestGarminScreensSet_Validation(t *testing.T) {
	t.Run("rejects an unknown sport", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolScreensSet, map[string]any{
			"device_id": 123, "sport": "quidditch", "page": 1, "fields": []string{"PACE"},
		})

		require.Contains(t, err.Error(), "unknown sport")
	})

	t.Run("rejects page zero", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolScreensSet, map[string]any{
			"device_id": 123, "sport": "running", "page": 0, "fields": []string{"PACE"},
		})

		require.Contains(t, err.Error(), "page must be 1 or greater")
	})

	t.Run("rejects an empty field list", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolScreensSet, map[string]any{
			"device_id": 123, "sport": "running", "page": 1, "fields": []string{},
		})

		require.Contains(t, err.Error(), "fields is required")
	})
}

func TestGarminLoginMFA(t *testing.T) {
	t.Run("rejects a code when no sign-in is pending", func(t *testing.T) {
		// The challenge lives in memory; a code arriving without one means tclaw restarted.
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolLoginMFA, map[string]any{
			"code": "123456",
		})

		require.Contains(t, err.Error(), "no sign-in is waiting for a code")
	})

	t.Run("rejects an empty code", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolLoginMFA, map[string]any{"code": "  "})

		require.Contains(t, err.Error(), "code is required")
	})
}

func TestGarminLogin_WithoutCredentials(t *testing.T) {
	t.Run("explains what is missing rather than attempting a sign-in", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolLogin, map[string]any{})

		require.Contains(t, err.Error(), "no Garmin credentials stored")
	})

	t.Run("persists credentials passed as arguments", func(t *testing.T) {
		// The stub transport makes the sign-in fail immediately, but the credentials must still be
		// stored so the user is not asked for them again.
		handler, store := setup(t)

		_ = callToolExpectError(t, handler, garmintools.ToolLogin, map[string]any{
			"email": "someone@example.com", "password": "hunter2",
		})

		require.Equal(t, "someone@example.com", store.data[garmintools.EmailStoreKey])
		require.Equal(t, "hunter2", store.data[garmintools.PasswordStoreKey])
	})
}

func TestGarminSettingsGet_RequiresSignIn(t *testing.T) {
	t.Run("points at the login tool when no token is stored", func(t *testing.T) {
		handler, _ := setup(t)

		err := callToolExpectError(t, handler, garmintools.ToolSettingsGet, map[string]any{
			"device_id": 123,
		})

		require.Contains(t, err.Error(), garmintools.ToolLogin)
	})
}

func TestRegisterTools(t *testing.T) {
	t.Run("registers every declared tool", func(t *testing.T) {
		handler, _ := setup(t)

		for _, name := range garmintools.ToolNames() {
			// Most of these fail for lack of credentials, which is fine — the point is that the
			// dispatcher knows the tool at all.
			_, err := handler.Call(context.Background(), name, json.RawMessage(`{}`))
			require.NotErrorAs(t, err, new(*mcp.ToolNotFoundError), "tool %s is not registered", name)
		}
	})
}

// --- helpers ---

func setup(t *testing.T) (*mcp.Handler, *memorySecretStore) {
	t.Helper()
	store := &memorySecretStore{data: map[string]string{}}
	handler := mcp.NewHandler()
	garmintools.RegisterTools(handler, garmintools.Deps{
		SecretStore: store,
		HTTPClient:  offlineClient(),
	})
	return handler, store
}

// offlineClient refuses every request. Tests must never reach Garmin: it would be slow and flaky,
// and sign-in attempts count against the real account's IP rate limit.
func offlineClient() *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network access is disabled in tests")
	})}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func callTool(t *testing.T, h *mcp.Handler, name string, args any) json.RawMessage {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	result, err := h.Call(context.Background(), name, argsJSON)
	require.NoError(t, err, "call %s", name)
	return result
}

func callToolExpectError(t *testing.T, h *mcp.Handler, name string, args any) error {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = h.Call(context.Background(), name, argsJSON)
	require.Error(t, err, "expected error from %s", name)
	return err
}

type memorySecretStore struct {
	data map[string]string
}

func (m *memorySecretStore) Get(_ context.Context, key string) (string, error) {
	return m.data[key], nil
}

func (m *memorySecretStore) Set(_ context.Context, key, value string) error {
	m.data[key] = value
	return nil
}

func (m *memorySecretStore) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}
