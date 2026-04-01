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
