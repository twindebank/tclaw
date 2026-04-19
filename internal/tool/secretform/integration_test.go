package secretform_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"tclaw/internal/libraries/credentialerror"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/secretform"
)

// TestCredentialErrorToFormFlow exercises the full integration:
//
//  1. A tool returns a CREDENTIALS_NEEDED error
//  2. The error is parsed to extract form fields
//  3. secret_form_request is called with those fields
//  4. The user submits the form via HTTP
//  5. secret_form_wait confirms submission
//  6. The tool is retried and now has credentials
//
// This simulates what the agent does when it sees CREDENTIALS_NEEDED.
func TestCredentialErrorToFormFlow(t *testing.T) {
	secrets := newMemorySecretStore()
	mcpHandler := mcp.NewHandler()

	var httpHandler http.Handler
	var httpMu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpMu.Lock()
		h := httpHandler
		httpMu.Unlock()
		if h != nil {
			h.ServeHTTP(w, r)
		} else {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	secretform.RegisterTools(mcpHandler, secretform.Deps{
		SecretStore: secrets,
		BaseURL:     ts.URL,
		RegisterHandler: func(pattern string, h http.Handler) {
			httpMu.Lock()
			httpHandler = h
			httpMu.Unlock()
		},
	})

	// Register a fake tool that requires credentials.
	mcpHandler.Register(
		mcp.ToolDef{
			Name:        "fake_service_search",
			Description: "A tool that needs credentials",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
		},
		func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			apiKey, _ := secrets.Get(ctx, "fake_api_key")
			apiSecret, _ := secrets.Get(ctx, "fake_api_secret")
			if apiKey == "" || apiSecret == "" {
				return nil, credentialerror.New(
					"Fake Service Setup",
					"Enter your Fake Service credentials",
					credentialerror.Field{Key: "fake_api_key", Label: "API Key"},
					credentialerror.Field{Key: "fake_api_secret", Label: "API Secret"},
				)
			}
			return json.Marshal(map[string]string{"status": "ok", "data": "results for " + apiKey})
		},
	)

	ctx := context.Background()

	// Step 1: Call the tool — it fails with CREDENTIALS_NEEDED.
	args, _ := json.Marshal(map[string]any{"query": "test"})
	_, err := mcpHandler.Call(ctx, "fake_service_search", args)
	require.Error(t, err)
	require.Contains(t, err.Error(), "CREDENTIALS_NEEDED")

	// Step 2: Parse the error (simulating what the agent does).
	errMsg := err.Error()
	require.True(t, strings.HasPrefix(errMsg, "CREDENTIALS_NEEDED"))

	fieldsIdx := strings.Index(errMsg, "fields: ")
	require.Greater(t, fieldsIdx, 0)
	fieldsJSON := errMsg[fieldsIdx+len("fields: "):]

	var fields []credentialerror.Field
	require.NoError(t, json.Unmarshal([]byte(fieldsJSON), &fields))
	require.Len(t, fields, 2)

	// Step 3: Call secret_form_request with the extracted fields.
	formFields := make([]map[string]any, len(fields))
	for i, f := range fields {
		formFields[i] = map[string]any{"key": f.Key, "label": f.Label}
		if f.Description != "" {
			formFields[i]["description"] = f.Description
		}
	}
	requestArgs, _ := json.Marshal(map[string]any{
		"title":  "Fake Service Setup",
		"fields": formFields,
	})
	requestResult, err := mcpHandler.Call(ctx, "secret_form_request", requestArgs)
	require.NoError(t, err)

	var reqResp map[string]string
	require.NoError(t, json.Unmarshal(requestResult, &reqResp))
	requestID := reqResp["request_id"]
	verifyCode := reqResp["verify_code"]
	formURL := reqResp["url"]
	require.NotEmpty(t, requestID)
	require.NotEmpty(t, verifyCode)

	// Step 4: User submits the form via HTTP.
	resp, err := http.PostForm(formURL, url.Values{
		"_verify_code":    {verifyCode},
		"fake_api_key":    {"my-key-123"},
		"fake_api_secret": {"my-secret-456"},
	})
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Step 5: secret_form_wait confirms.
	waitArgs, _ := json.Marshal(map[string]any{"request_id": requestID})
	waitResult, err := mcpHandler.Call(ctx, "secret_form_wait", waitArgs)
	require.NoError(t, err)

	var waitResp map[string]any
	require.NoError(t, json.Unmarshal(waitResult, &waitResp))
	require.Equal(t, "submitted", waitResp["status"])

	// Step 6: Retry the original tool — now succeeds.
	result, err := mcpHandler.Call(ctx, "fake_service_search", args)
	require.NoError(t, err)

	var toolResp map[string]string
	require.NoError(t, json.Unmarshal(result, &toolResp))
	require.Equal(t, "ok", toolResp["status"])
	require.Equal(t, "results for my-key-123", toolResp["data"])
}

// TestSecretFormWait_CLITimeoutCancellation reproduces the production bug
// where the Claude CLI aborts MCP tool calls at ~60s. Before the fix,
// secret_form_wait blocked until the 10-minute TTL and the CLI cancelled
// it with "wait cancelled" — the user lost their form-fill mid-submit.
//
// After the fix, each wait call returns within ~45s with status
// "still_waiting", and the agent loops without hitting the CLI timeout.
func TestSecretFormWait_CLITimeoutCancellation(t *testing.T) {
	// Drop the per-call window so this suite runs in milliseconds instead
	// of the 45s production default. The behaviour under test (still_waiting
	// before the CLI cancels, loop-and-complete, immediate unblock on
	// submission) doesn't depend on the absolute duration.
	const testWait = 200 * time.Millisecond
	restore := secretform.SetMaxWaitPerCall(testWait)
	t.Cleanup(restore)

	t.Run("wait returns still_waiting before deadline", func(t *testing.T) {
		h := setupWaitHarness(t)
		form := createPendingForm(t, h.handler)

		// Mirror the CLI's tool-call behavior: enforce an outer deadline
		// that's comfortably longer than the per-call window.
		outerDeadline := 2 * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), outerDeadline)
		defer cancel()

		start := time.Now()
		waitArgs, _ := json.Marshal(map[string]any{"request_id": form.ID})
		result, err := h.handler.Call(ctx, "secret_form_wait", waitArgs)
		elapsed := time.Since(start)

		require.NoError(t, err, "wait must NOT return an error within CLI deadline — got %v", err)
		require.Less(t, elapsed, outerDeadline,
			"wait must return before outer deadline; elapsed=%s deadline=%s", elapsed, outerDeadline)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(result, &resp))
		require.Equal(t, "still_waiting", resp["status"])
		require.Equal(t, form.ID, resp["request_id"],
			"response must echo request_id so the agent can loop")
	})

	t.Run("follow-up call returns submitted after form submission", func(t *testing.T) {
		h := setupWaitHarness(t)
		form := createPendingForm(t, h.handler)

		// First wait returns still_waiting.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		waitArgs, _ := json.Marshal(map[string]any{"request_id": form.ID})
		result, err := h.handler.Call(ctx, "secret_form_wait", waitArgs)
		require.NoError(t, err)

		var firstResp map[string]any
		require.NoError(t, json.Unmarshal(result, &firstResp))
		require.Equal(t, "still_waiting", firstResp["status"])

		// User submits the form between tclaw-side wait calls.
		submitForm(t, h, form)

		// Second wait call returns submitted immediately.
		start := time.Now()
		result, err = h.handler.Call(context.Background(), "secret_form_wait", waitArgs)
		require.NoError(t, err)
		require.Less(t, time.Since(start), 500*time.Millisecond,
			"follow-up wait with already-submitted form must return immediately")

		var secondResp map[string]any
		require.NoError(t, json.Unmarshal(result, &secondResp))
		require.Equal(t, "submitted", secondResp["status"])
	})

	t.Run("wait unblocks immediately if form is submitted during the window", func(t *testing.T) {
		h := setupWaitHarness(t)
		form := createPendingForm(t, h.handler)

		// Submit the form shortly after the wait starts.
		go func() {
			time.Sleep(20 * time.Millisecond)
			submitForm(t, h, form)
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		start := time.Now()
		waitArgs, _ := json.Marshal(map[string]any{"request_id": form.ID})
		result, err := h.handler.Call(ctx, "secret_form_wait", waitArgs)
		elapsed := time.Since(start)

		require.NoError(t, err)
		require.Less(t, elapsed, 500*time.Millisecond,
			"wait must unblock ~immediately when the form is submitted")

		var resp map[string]any
		require.NoError(t, json.Unmarshal(result, &resp))
		require.Equal(t, "submitted", resp["status"])
	})
}

// --- helpers ---

type waitHarness struct {
	handler *mcp.Handler
	secrets *memorySecretStore
	server  *httptest.Server
}

func setupWaitHarness(t *testing.T) *waitHarness {
	t.Helper()
	secrets := newMemorySecretStore()
	handler := mcp.NewHandler()

	var httpHandler http.Handler
	var httpMu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpMu.Lock()
		h := httpHandler
		httpMu.Unlock()
		if h != nil {
			h.ServeHTTP(w, r)
		} else {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	secretform.RegisterTools(handler, secretform.Deps{
		SecretStore: secrets,
		BaseURL:     ts.URL,
		RegisterHandler: func(_ string, h http.Handler) {
			httpMu.Lock()
			httpHandler = h
			httpMu.Unlock()
		},
	})
	return &waitHarness{handler: handler, secrets: secrets, server: ts}
}

type pendingForm struct {
	ID         string
	VerifyCode string
	URL        string
}

func createPendingForm(t *testing.T, handler *mcp.Handler) pendingForm {
	t.Helper()
	args, _ := json.Marshal(map[string]any{
		"title": "Test",
		"fields": []map[string]any{
			{"key": "api_key", "label": "API Key"},
		},
	})
	result, err := handler.Call(context.Background(), "secret_form_request", args)
	require.NoError(t, err)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(result, &resp))
	require.NotEmpty(t, resp["request_id"])
	require.NotEmpty(t, resp["verify_code"])
	require.NotEmpty(t, resp["url"])
	return pendingForm{ID: resp["request_id"], VerifyCode: resp["verify_code"], URL: resp["url"]}
}

func submitForm(t *testing.T, h *waitHarness, form pendingForm) {
	t.Helper()
	resp, err := http.PostForm(form.URL, url.Values{
		"_verify_code": {form.VerifyCode},
		"api_key":      {"test-value"},
	})
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
