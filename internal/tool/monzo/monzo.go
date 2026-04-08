package monzo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"tclaw/internal/credential"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/providerutil"
)

const baseURL = "https://api.monzo.com"

const (
	// ClientIDStoreKey is the secret store key for the Monzo OAuth client ID.
	ClientIDStoreKey = "monzo_client_id"

	// ClientSecretStoreKey is the secret store key for the Monzo OAuth client secret.
	ClientSecretStoreKey = "monzo_client_secret"
)

// Deps holds dependencies for a single Monzo credential set.
type Deps = providerutil.Deps

// RegisterTools registers (or re-registers) the Monzo tools with handlers
// that resolve the credential set dynamically from depsMap.
func RegisterTools(handler *mcp.Handler, depsMap map[credential.CredentialSetID]Deps) {
	setIDs := make([]credential.CredentialSetID, 0, len(depsMap))
	for id := range depsMap {
		setIDs = append(setIDs, id)
	}

	defs := ToolDefs(setIDs)
	handler.Register(defs[0], listAccountsHandler(depsMap))
	handler.Register(defs[1], getBalanceHandler(depsMap))
	handler.Register(defs[2], listPotsHandler(depsMap))
	handler.Register(defs[3], listTransactionsHandler(depsMap))
	handler.Register(defs[4], getTransactionHandler(depsMap))
}

// UnregisterTools removes the Monzo tools from the handler.
func UnregisterTools(handler *mcp.Handler) {
	handler.Unregister(ToolListAccounts)
	handler.Unregister(ToolGetBalance)
	handler.Unregister(ToolListPots)
	handler.Unregister(ToolListTransactions)
	handler.Unregister(ToolGetTransaction)
}

// resolveDeps looks up the Deps for a credential set ID from the tool args.
func resolveDeps(depsMap map[credential.CredentialSetID]Deps, idStr string) (Deps, error) {
	return providerutil.ResolveDeps(depsMap, idStr)
}

// accessToken gets a valid access token for the credential set, refreshing if needed.
func accessToken(ctx context.Context, deps Deps) (string, error) {
	return providerutil.AccessToken(ctx, deps)
}

// apiGet makes a GET request to the Monzo API.
func apiGet(ctx context.Context, deps Deps, path string, query url.Values) (json.RawMessage, error) {
	token, err := accessToken(ctx, deps)
	if err != nil {
		return nil, err
	}

	reqURL := baseURL + path
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("monzo API %s: %w", path, err)
	}
	defer resp.Body.Close()

	// Cap response body to 5 MiB to prevent memory exhaustion from oversized payloads.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Provide an actionable message for SCA verification errors instead of the raw API response.
		if isVerificationRequired(body) {
			return nil, fmt.Errorf("Monzo requires in-app verification to access transactions older than 90 days. Open your Monzo app to approve extended access, or use a `since` date within the last 90 days.")
		}
		return nil, fmt.Errorf("monzo API %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	return json.RawMessage(body), nil
}

// isVerificationRequired checks whether a Monzo error response indicates SCA verification is needed.
func isVerificationRequired(body []byte) bool {
	var errResp struct {
		Code string `json:"code"`
	}
	return json.Unmarshal(body, &errResp) == nil && errResp.Code == "forbidden.verification_required"
}
