package secretform_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/mcp"
	"tclaw/tool/secretform"
)

func TestSecretFormRequest(t *testing.T) {
	t.Run("returns url, request_id, and verify_code", func(t *testing.T) {
		h, _ := setup(t)

		result := callTool(t, h, "secret_form_request", map[string]any{
			"title": "GitHub Setup",
			"fields": []map[string]any{
				{"key": "github_token", "label": "Personal Access Token"},
			},
		})

		var got map[string]string
		require.NoError(t, json.Unmarshal(result, &got))
		require.NotEmpty(t, got["request_id"])
		require.Contains(t, got["url"], "/secret-form/"+got["request_id"])
		require.Len(t, got["verify_code"], 6, "verify code should be 6 digits")
	})

	t.Run("supports multiple fields", func(t *testing.T) {
		h, _ := setup(t)

		result := callTool(t, h, "secret_form_request", map[string]any{
			"title":       "OAuth Setup",
			"description": "Enter your OAuth credentials",
			"fields": []map[string]any{
				{"key": "client_id", "label": "Client ID", "secret": false},
				{"key": "client_secret", "label": "Client Secret"},
			},
		})

		var got map[string]string
		require.NoError(t, json.Unmarshal(result, &got))
		require.NotEmpty(t, got["request_id"])
	})

	t.Run("rejects missing title", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "secret_form_request", map[string]any{
			"fields": []map[string]any{
				{"key": "github_token", "label": "Token"},
			},
		})
		require.Contains(t, err.Error(), "title")
	})

	t.Run("rejects empty fields", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "secret_form_request", map[string]any{
			"title":  "Test",
			"fields": []map[string]any{},
		})
		require.Contains(t, err.Error(), "at least one field")
	})

	t.Run("rejects field without key", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "secret_form_request", map[string]any{
			"title": "Test",
			"fields": []map[string]any{
				{"label": "Token"},
			},
		})
		require.Contains(t, err.Error(), "key is required")
	})

	t.Run("rejects field without label", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "secret_form_request", map[string]any{
			"title": "Test",
			"fields": []map[string]any{
				{"key": "github_token"},
			},
		})
		require.Contains(t, err.Error(), "label is required")
	})

	t.Run("rejects invalid key characters", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "secret_form_request", map[string]any{
			"title": "Test",
			"fields": []map[string]any{
				{"key": "../../../etc/passwd", "label": "Evil"},
			},
		})
		require.Contains(t, err.Error(), "invalid characters")
	})

	t.Run("rejects uppercase key", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "secret_form_request", map[string]any{
			"title": "Test",
			"fields": []map[string]any{
				{"key": "GitHub_Token", "label": "Token"},
			},
		})
		require.Contains(t, err.Error(), "invalid characters")
	})

	t.Run("rejects reserved key", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "secret_form_request", map[string]any{
			"title": "Test",
			"fields": []map[string]any{
				{"key": "anthropic_api_key", "label": "API Key"},
			},
		})
		require.Contains(t, err.Error(), "reserved")
	})

	t.Run("rejects too many fields", func(t *testing.T) {
		h, _ := setup(t)

		fields := make([]map[string]any, 25)
		for i := range fields {
			fields[i] = map[string]any{"key": "key_" + strings.Repeat("x", i), "label": "Label"}
		}
		// Keys need to be unique and valid — just use indexed names.
		for i := range fields {
			fields[i]["key"] = "key" + string(rune('a'+i))
		}

		err := callToolExpectError(t, h, "secret_form_request", map[string]any{
			"title":  "Test",
			"fields": fields,
		})
		require.Contains(t, err.Error(), "too many fields")
	})
}

func TestSecretFormWait(t *testing.T) {
	t.Run("returns submitted after form completion", func(t *testing.T) {
		h, ts := setup(t)

		requestResult := callTool(t, h, "secret_form_request", map[string]any{
			"title": "Test",
			"fields": []map[string]any{
				{"key": "my_key", "label": "My Key"},
			},
		})
		var reqResp map[string]string
		require.NoError(t, json.Unmarshal(requestResult, &reqResp))
		requestID := reqResp["request_id"]
		verifyCode := reqResp["verify_code"]

		// Submit via HTTP with the correct verify code.
		formURL := ts.URL + "/secret-form/" + requestID
		resp, err := http.PostForm(formURL, url.Values{
			"_verify_code": {verifyCode},
			"my_key":       {"secret-value-123"},
		})
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Wait should return immediately since the form is already submitted.
		waitResult := callTool(t, h, "secret_form_wait", map[string]any{
			"request_id": requestID,
		})
		var waitResp map[string]any
		require.NoError(t, json.Unmarshal(waitResult, &waitResp))
		require.Equal(t, "submitted", waitResp["status"])
		keys := waitResp["keys"].([]any)
		require.Len(t, keys, 1)
		require.Equal(t, "my_key", keys[0])
	})

	t.Run("returns error for unknown request_id", func(t *testing.T) {
		h, _ := setup(t)

		err := callToolExpectError(t, h, "secret_form_wait", map[string]any{
			"request_id": "nonexistent",
		})
		require.Contains(t, err.Error(), "unknown request ID")
	})
}

func TestHTTPFormHandler(t *testing.T) {
	t.Run("GET renders form with fields and verify code input", func(t *testing.T) {
		h, ts := setup(t)

		requestResult := callTool(t, h, "secret_form_request", map[string]any{
			"title":       "GitHub Setup",
			"description": "Enter your token",
			"fields": []map[string]any{
				{"key": "github_token", "label": "Personal Access Token"},
				{"key": "github_org", "label": "Organization", "secret": false, "required": false},
			},
		})
		var reqResp map[string]string
		require.NoError(t, json.Unmarshal(requestResult, &reqResp))

		resp, err := http.Get(ts.URL + "/secret-form/" + reqResp["request_id"])
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		bodyStr := string(body)
		require.Contains(t, bodyStr, "GitHub Setup")
		require.Contains(t, bodyStr, "Enter your token")
		require.Contains(t, bodyStr, "Personal Access Token")
		require.Contains(t, bodyStr, "Organization")
		require.Contains(t, bodyStr, "_verify_code")
		require.Contains(t, bodyStr, "Verification Code")
	})

	t.Run("GET returns security headers", func(t *testing.T) {
		h, ts := setup(t)

		requestResult := callTool(t, h, "secret_form_request", map[string]any{
			"title":  "Test",
			"fields": []map[string]any{{"key": "test_key", "label": "Test"}},
		})
		var reqResp map[string]string
		require.NoError(t, json.Unmarshal(requestResult, &reqResp))

		resp, err := http.Get(ts.URL + "/secret-form/" + reqResp["request_id"])
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
		require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		require.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
		require.Equal(t, "noindex, nofollow", resp.Header.Get("X-Robots-Tag"))
		require.Contains(t, resp.Header.Get("Cache-Control"), "no-store")
		require.NotEmpty(t, resp.Header.Get("Content-Security-Policy"))
	})

	t.Run("GET unknown state returns 404", func(t *testing.T) {
		_, ts := setup(t)

		resp, err := http.Get(ts.URL + "/secret-form/nonexistent")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("POST stores values with correct verify code", func(t *testing.T) {
		h, ts := setup(t)

		requestResult := callTool(t, h, "secret_form_request", map[string]any{
			"title": "OAuth Config",
			"fields": []map[string]any{
				{"key": "client_id", "label": "Client ID", "secret": false},
				{"key": "client_secret", "label": "Client Secret"},
			},
		})
		var reqResp map[string]string
		require.NoError(t, json.Unmarshal(requestResult, &reqResp))

		formURL := ts.URL + "/secret-form/" + reqResp["request_id"]
		resp, err := http.PostForm(formURL, url.Values{
			"_verify_code":  {reqResp["verify_code"]},
			"client_id":     {"my-app-id"},
			"client_secret": {"super-secret-value"},
		})
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "Submitted")

		// Verify secrets were stored.
		secrets := getSecretStore(t, ts)
		clientID, err := secrets.Get(context.Background(), "client_id")
		require.NoError(t, err)
		require.Equal(t, "my-app-id", clientID)

		clientSecret, err := secrets.Get(context.Background(), "client_secret")
		require.NoError(t, err)
		require.Equal(t, "super-secret-value", clientSecret)
	})

	t.Run("POST rejects wrong verify code", func(t *testing.T) {
		h, ts := setup(t)

		requestResult := callTool(t, h, "secret_form_request", map[string]any{
			"title":  "Test",
			"fields": []map[string]any{{"key": "test_key", "label": "Test"}},
		})
		var reqResp map[string]string
		require.NoError(t, json.Unmarshal(requestResult, &reqResp))

		formURL := ts.URL + "/secret-form/" + reqResp["request_id"]
		resp, err := http.PostForm(formURL, url.Values{
			"_verify_code": {"000000"},
			"test_key":     {"value"},
		})
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "Invalid verification code")

		// Verify secret was NOT stored.
		secrets := getSecretStore(t, ts)
		val, err := secrets.Get(context.Background(), "test_key")
		require.NoError(t, err)
		require.Empty(t, val, "secret should not be stored with wrong verify code")
	})

	t.Run("POST rejects missing required field", func(t *testing.T) {
		h, ts := setup(t)

		requestResult := callTool(t, h, "secret_form_request", map[string]any{
			"title": "Test",
			"fields": []map[string]any{
				{"key": "required_key", "label": "Required Field"},
			},
		})
		var reqResp map[string]string
		require.NoError(t, json.Unmarshal(requestResult, &reqResp))

		formURL := ts.URL + "/secret-form/" + reqResp["request_id"]
		resp, err := http.PostForm(formURL, url.Values{
			"_verify_code": {reqResp["verify_code"]},
			// required_key is intentionally missing
		})
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("POST is single-use", func(t *testing.T) {
		h, ts := setup(t)

		requestResult := callTool(t, h, "secret_form_request", map[string]any{
			"title":  "Test",
			"fields": []map[string]any{{"key": "test_key", "label": "Test"}},
		})
		var reqResp map[string]string
		require.NoError(t, json.Unmarshal(requestResult, &reqResp))
		formURL := ts.URL + "/secret-form/" + reqResp["request_id"]

		// First POST succeeds.
		resp, err := http.PostForm(formURL, url.Values{
			"_verify_code": {reqResp["verify_code"]},
			"test_key":     {"value1"},
		})
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Second POST returns 404.
		resp2, err := http.PostForm(formURL, url.Values{
			"_verify_code": {reqResp["verify_code"]},
			"test_key":     {"value2"},
		})
		require.NoError(t, err)
		resp2.Body.Close()
		require.Equal(t, http.StatusNotFound, resp2.StatusCode)
	})
}

// --- helpers ---

type testServer struct {
	secrets *memorySecretStore
}

func setup(t *testing.T) (*mcp.Handler, *httptest.Server) {
	t.Helper()

	secrets := newMemorySecretStore()
	handler := mcp.NewHandler()

	var httpHandler http.Handler
	var httpHandlerMu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpHandlerMu.Lock()
		h := httpHandler
		httpHandlerMu.Unlock()
		if h != nil {
			h.ServeHTTP(w, r)
		} else {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	t.Cleanup(func() {
		testServers.Delete(ts.URL)
	})
	testServers.Store(ts.URL, &testServer{secrets: secrets})

	secretform.RegisterTools(handler, secretform.Deps{
		SecretStore: secrets,
		BaseURL:     ts.URL,
		RegisterHandler: func(pattern string, h http.Handler) {
			httpHandlerMu.Lock()
			httpHandler = h
			httpHandlerMu.Unlock()
		},
	})

	return handler, ts
}

var testServers sync.Map

func getSecretStore(t *testing.T, ts *httptest.Server) *memorySecretStore {
	t.Helper()
	v, ok := testServers.Load(ts.URL)
	require.True(t, ok, "test server not found")
	return v.(*testServer).secrets
}

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

func newMemorySecretStore() *memorySecretStore {
	return &memorySecretStore{data: make(map[string]string)}
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
