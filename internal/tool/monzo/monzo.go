package monzo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tclaw/internal/credential"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/providerutil"
)

// baseURL is a var so tests can point it at a local server.
var baseURL = "https://api.monzo.com"

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

	handlers := map[string]mcp.ToolHandler{
		ToolWhoAmI:               whoAmIHandler(depsMap),
		ToolListAccounts:         listAccountsHandler(depsMap),
		ToolGetBalance:           getBalanceHandler(depsMap),
		ToolListPots:             listPotsHandler(depsMap),
		ToolDepositIntoPot:       potTransferHandler(depsMap, potDeposit),
		ToolWithdrawFromPot:      potTransferHandler(depsMap, potWithdraw),
		ToolListTransactions:     listTransactionsHandler(depsMap),
		ToolGetTransaction:       getTransactionHandler(depsMap),
		ToolAnnotateTransaction:  annotateTransactionHandler(depsMap),
		ToolCreateFeedItem:       createFeedItemHandler(depsMap),
		ToolRegisterAttachment:   registerAttachmentHandler(depsMap),
		ToolDeregisterAttachment: deregisterAttachmentHandler(depsMap),
		ToolGetReceipt:           receiptByExternalIDHandler(depsMap, http.MethodGet),
		ToolSetReceipt:           setReceiptHandler(depsMap),
		ToolDeleteReceipt:        receiptByExternalIDHandler(depsMap, http.MethodDelete),
		ToolListWebhooks:         listWebhooksHandler(depsMap),
		ToolRegisterWebhook:      registerWebhookHandler(depsMap),
		ToolDeleteWebhook:        deleteWebhookHandler(depsMap),
	}
	for _, def := range ToolDefs(setIDs) {
		h, ok := handlers[def.Name]
		if !ok {
			slog.Error("monzo: tool has no handler, skipping", "tool", def.Name)
			continue
		}
		handler.Register(def, h)
	}
}

// UnregisterTools removes the Monzo tools from the handler.
func UnregisterTools(handler *mcp.Handler) {
	for _, name := range ToolNames() {
		handler.Unregister(name)
	}
}

// resolveDeps looks up the Deps for a credential set ID from the tool args.
func resolveDeps(depsMap map[credential.CredentialSetID]Deps, idStr string) (Deps, error) {
	return providerutil.ResolveDeps(depsMap, idStr)
}

// accessToken gets a valid access token for the credential set, refreshing if needed.
func accessToken(ctx context.Context, deps Deps) (string, error) {
	return providerutil.AccessToken(ctx, deps)
}

type apiRequestParams struct {
	Method string
	Path   string
	Query  url.Values

	// Form is sent as a URL-encoded body when set. Set at most one of Form and JSON.
	Form url.Values

	JSON json.RawMessage
}

// apiRequest makes a request to the Monzo API and returns the raw JSON body.
func apiRequest(ctx context.Context, deps Deps, p apiRequestParams) (json.RawMessage, error) {
	token, err := accessToken(ctx, deps)
	if err != nil {
		return nil, err
	}

	reqURL := baseURL + p.Path
	if len(p.Query) > 0 {
		reqURL += "?" + p.Query.Encode()
	}

	var body io.Reader
	var contentType string
	switch {
	case len(p.Form) > 0 && len(p.JSON) > 0:
		return nil, fmt.Errorf("monzo API %s: request has both a form and a JSON body", p.Path)
	case len(p.Form) > 0:
		body = strings.NewReader(p.Form.Encode())
		contentType = "application/x-www-form-urlencoded"
	case len(p.JSON) > 0:
		body = bytes.NewReader(p.JSON)
		contentType = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, p.Method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	rsp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("monzo API %s: %w", p.Path, err)
	}
	defer rsp.Body.Close()

	// Cap response body to 5 MiB to prevent memory exhaustion from oversized payloads.
	rspBody, err := io.ReadAll(io.LimitReader(rsp.Body, 5<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if rsp.StatusCode != http.StatusOK {
		// Provide an actionable message for SCA verification errors instead of the raw API response.
		if isVerificationRequired(rspBody) {
			return nil, fmt.Errorf("Monzo requires in-app verification to access transactions older than 90 days. Open your Monzo app to approve extended access, or use a `since` date within the last 90 days.")
		}
		return nil, fmt.Errorf("monzo API %s returned %d: %s", p.Path, rsp.StatusCode, string(rspBody))
	}

	return json.RawMessage(rspBody), nil
}

// isVerificationRequired checks whether a Monzo error response indicates SCA verification is needed.
func isVerificationRequired(body []byte) bool {
	var errResp struct {
		Code string `json:"code"`
	}
	return json.Unmarshal(body, &errResp) == nil && errResp.Code == "forbidden.verification_required"
}
