package monzo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"tclaw/connection"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/providerutil"
)

const baseURL = "https://api.monzo.com"

const (
	// ClientIDStoreKey is the secret store key for the Monzo OAuth client ID.
	ClientIDStoreKey = "monzo_client_id"

	// ClientSecretStoreKey is the secret store key for the Monzo OAuth client secret.
	ClientSecretStoreKey = "monzo_client_secret"
)

// Deps holds dependencies for a single Monzo connection.
type Deps = providerutil.Deps

// SetCredentialsDeps holds dependencies for the monzo_set_credentials tool.
type SetCredentialsDeps struct {
	SecretStore         secret.Store
	OnCredentialsStored func()

	// RedirectURL is the OAuth callback URL to include in the response so the
	// agent can guide the user through developer portal setup.
	RedirectURL string
}

// RegisterSetCredentialsTool registers the monzo_set_credentials tool.
// Always visible so the agent can discover Monzo and set up credentials at runtime.
func RegisterSetCredentialsTool(handler *mcp.Handler, deps SetCredentialsDeps) {
	handler.Register(setCredentialsDef, setCredentialsHandler(deps))
}

// RegisterTools registers (or re-registers) the Monzo tools with handlers
// that resolve the connection dynamically from connMap.
func RegisterTools(handler *mcp.Handler, connMap map[connection.ConnectionID]Deps) {
	connIDs := make([]connection.ConnectionID, 0, len(connMap))
	for id := range connMap {
		connIDs = append(connIDs, id)
	}

	defs := ToolDefs(connIDs)
	handler.Register(defs[0], listAccountsHandler(connMap))
	handler.Register(defs[1], getBalanceHandler(connMap))
	handler.Register(defs[2], listPotsHandler(connMap))
	handler.Register(defs[3], listTransactionsHandler(connMap))
	handler.Register(defs[4], getTransactionHandler(connMap))
}

// UnregisterTools removes the Monzo tools from the handler.
func UnregisterTools(handler *mcp.Handler) {
	handler.Unregister("monzo_list_accounts")
	handler.Unregister("monzo_get_balance")
	handler.Unregister("monzo_list_pots")
	handler.Unregister("monzo_list_transactions")
	handler.Unregister("monzo_get_transaction")
}

// resolveDeps looks up the Deps for a connection ID from the tool args.
func resolveDeps(connMap map[connection.ConnectionID]Deps, connIDStr string) (Deps, error) {
	return providerutil.ResolveDeps(connMap, connIDStr)
}

// accessToken gets a valid access token for the connection, refreshing if needed.
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("monzo API %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	return json.RawMessage(body), nil
}
