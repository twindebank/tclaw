package tfl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"tclaw/libraries/secret"
	"tclaw/mcp"
)

const (
	baseURL = "https://api.tfl.gov.uk"

	// APIKeyStoreKey is the secret store key for the TfL API key.
	APIKeyStoreKey = "tfl_api_key"
)

// Deps holds dependencies for TfL tools.
type Deps struct {
	SecretStore secret.Store
}

// RegisterTools registers the TfL tools on the handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	for _, def := range toolDefs {
		handler.Register(def, makeHandler(def.Name, deps))
	}
}

// UnregisterTools removes the TfL tools from the handler.
func UnregisterTools(handler *mcp.Handler) {
	for _, def := range toolDefs {
		handler.Unregister(def.Name)
	}
}

// apiGet makes a GET request to the TfL API, adding the API key if available.
func apiGet(ctx context.Context, deps Deps, path string, query url.Values) (json.RawMessage, error) {
	if query == nil {
		query = url.Values{}
	}

	// Add API key if stored — TfL works without one but is rate-limited.
	apiKey, _ := deps.SecretStore.Get(ctx, APIKeyStoreKey)
	if apiKey != "" {
		query.Set("app_key", apiKey)
	}

	reqURL := baseURL + path
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tfl API %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("TfL API rate limit exceeded — provide an api_key to increase the limit (register free at https://api-portal.tfl.gov.uk/products)")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tfl API %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	return json.RawMessage(body), nil
}
