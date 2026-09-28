package monzo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"tclaw/internal/claudecli"
	"tclaw/internal/credential"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/toolgroup"
)

func TestListPotsHandler(t *testing.T) {
	t.Run("asks for the given account's pots", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := listPotsHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"account_id":     "acc_joint",
		}))
		require.NoError(t, err)

		require.Equal(t, http.MethodGet, env.last.Method, "method")
		require.Equal(t, "/pots", env.last.Path, "path")
		require.Equal(t, "acc_joint", env.last.Query.Get("current_account_id"), "account sent to Monzo")
	})
}

func TestPotTransferHandler(t *testing.T) {
	t.Run("deposit sends the source account", func(t *testing.T) {
		env := setupMonzo(t)

		rsp, err := potTransferHandler(env.depsMap, potDeposit)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"pot_id":         "pot_1",
			"account_id":     "acc_joint",
			"amount":         2500,
			"dedupe_id":      "transfer-1",
		}))
		require.NoError(t, err)
		require.JSONEq(t, `{"id":"pot_1"}`, string(rsp), "returns Monzo's response")

		require.Equal(t, http.MethodPut, env.last.Method, "method")
		require.Equal(t, "/pots/pot_1/deposit", env.last.Path, "path")
		require.Equal(t, url.Values{
			"source_account_id": {"acc_joint"},
			"amount":            {"2500"},
			"dedupe_id":         {"transfer-1"},
		}, env.last.Form, "form body")
	})

	t.Run("withdraw sends the destination account", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := potTransferHandler(env.depsMap, potWithdraw)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"pot_id":         "pot_1",
			"account_id":     "acc_main",
			"amount":         100,
			"dedupe_id":      "transfer-2",
		}))
		require.NoError(t, err)

		require.Equal(t, "/pots/pot_1/withdraw", env.last.Path, "path")
		require.Equal(t, url.Values{
			"destination_account_id": {"acc_main"},
			"amount":                 {"100"},
			"dedupe_id":              {"transfer-2"},
		}, env.last.Form, "form body")
	})

	t.Run("rejects bad args without calling Monzo", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := potTransferHandler(env.depsMap, potDeposit)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"amount":         0,
		}))
		require.Error(t, err)
		require.Equal(t, "pot_id is required\naccount_id is required\n"+
			"amount must be a positive number of pence, got 0\ndedupe_id is required", err.Error())
		require.Equal(t, "", env.last.Path, "no request made")
	})

	t.Run("passes on Monzo's error", func(t *testing.T) {
		env := setupMonzo(t)
		env.status = http.StatusForbidden

		_, err := potTransferHandler(env.depsMap, potWithdraw)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"pot_id":         "pot_1",
			"account_id":     "acc_main",
			"amount":         100,
			"dedupe_id":      "transfer-3",
		}))
		require.Error(t, err)
		require.Equal(t, `monzo API /pots/pot_1/withdraw returned 403: {"id":"pot_1"}`, err.Error())
	})

	t.Run("asks for in-app approval when Monzo wants verification", func(t *testing.T) {
		env := setupMonzo(t)
		env.status = http.StatusForbidden
		env.rspBody = `{"code":"forbidden.verification_required"}`

		_, err := potTransferHandler(env.depsMap, potDeposit)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"pot_id":         "pot_1",
			"account_id":     "acc_main",
			"amount":         100,
			"dedupe_id":      "transfer-4",
		}))
		require.Error(t, err)
		require.Equal(t, "Monzo requires in-app verification before this app can continue. "+
			"Open your Monzo app and approve access, then try again.", err.Error())
	})
}

func TestRegisterTools(t *testing.T) {
	t.Run("registers every tool with a valid schema", func(t *testing.T) {
		env := setupMonzo(t)
		handler := mcp.NewHandler()

		RegisterTools(handler, env.depsMap)

		defs := handler.ListTools()
		names := make([]string, 0, len(defs))
		for _, def := range defs {
			names = append(names, def.Name)
			var schema map[string]any
			require.NoError(t, json.Unmarshal(def.InputSchema, &schema), "schema for %s", def.Name)
		}
		require.ElementsMatch(t, ToolNames(), names, "registered tools")
	})
}

func TestListAccountsHandler(t *testing.T) {
	t.Run("passes the account type filter", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := listAccountsHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"account_type":   "uk_retail_joint",
		}))
		require.NoError(t, err)

		require.Equal(t, "uk_retail_joint", env.last.Query.Get("account_type"), "account type sent to Monzo")
	})
}

func TestListTransactionsHandler(t *testing.T) {
	t.Run("passes before", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := listTransactionsHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"account_id":     "acc_main",
			"before":         "2026-09-01T00:00:00Z",
		}))
		require.NoError(t, err)

		require.Equal(t, "2026-09-01T00:00:00Z", env.last.Query.Get("before"), "before sent to Monzo")
	})

	t.Run("explains the 90-day limit when Monzo wants verification", func(t *testing.T) {
		env := setupMonzo(t)
		env.status = http.StatusForbidden
		env.rspBody = `{"code":"forbidden.verification_required"}`

		_, err := listTransactionsHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"account_id":     "acc_main",
		}))
		require.Error(t, err)
		require.Equal(t, "Monzo requires in-app verification to access transactions older than 90 days. "+
			"Open your Monzo app to approve extended access, or use a `since` date within the last 90 days.", err.Error())
	})
}

func TestAnnotateTransactionHandler(t *testing.T) {
	t.Run("sends metadata keys as a form", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := annotateTransactionHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"transaction_id": "tx_1",
			"metadata":       map[string]string{"notes": "dinner", "old": ""},
		}))
		require.NoError(t, err)

		require.Equal(t, http.MethodPatch, env.last.Method, "method")
		require.Equal(t, "/transactions/tx_1", env.last.Path, "path")
		require.Equal(t, url.Values{"metadata[notes]": {"dinner"}, "metadata[old]": {""}}, env.last.Form, "form body")
	})
}

func TestCreateFeedItemHandler(t *testing.T) {
	t.Run("sends only the fields given", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := createFeedItemHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"account_id":     "acc_main",
			"title":          "Rent due",
			"image_url":      "https://example.com/icon.png",
			"body":           "Tomorrow",
		}))
		require.NoError(t, err)

		require.Equal(t, "/feed", env.last.Path, "path")
		require.Equal(t, url.Values{
			"account_id":        {"acc_main"},
			"type":              {"basic"},
			"params[title]":     {"Rent due"},
			"params[image_url]": {"https://example.com/icon.png"},
			"params[body]":      {"Tomorrow"},
		}, env.last.Form, "form body")
	})
}

func TestSetReceiptHandler(t *testing.T) {
	t.Run("sends the receipt as JSON", func(t *testing.T) {
		env := setupMonzo(t)
		receipt := `{"transaction_id":"tx_1","external_id":"r1","total":500,"currency":"GBP","items":[]}`

		_, err := setReceiptHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"receipt":        json.RawMessage(receipt),
		}))
		require.NoError(t, err)

		require.Equal(t, http.MethodPut, env.last.Method, "method")
		require.Equal(t, "/transaction-receipts", env.last.Path, "path")
		require.Equal(t, "application/json", env.last.ContentType, "content type")
		require.JSONEq(t, receipt, env.last.Body, "body")
	})
}

func TestReceiptByExternalIDHandler(t *testing.T) {
	t.Run("gets by external ID", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := receiptByExternalIDHandler(env.depsMap, receiptGet)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"external_id":    "r1",
		}))
		require.NoError(t, err)

		require.Equal(t, http.MethodGet, env.last.Method, "method")
		require.Equal(t, "/transaction-receipts", env.last.Path, "path")
		require.Equal(t, "r1", env.last.Query.Get("external_id"), "external ID sent to Monzo")
	})

	t.Run("deletes by external ID", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := receiptByExternalIDHandler(env.depsMap, http.MethodDelete)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"external_id":    "r1",
		}))
		require.NoError(t, err)

		require.Equal(t, http.MethodDelete, env.last.Method, "method")
		require.Equal(t, "r1", env.last.Query.Get("external_id"), "external ID sent to Monzo")
	})
}

func TestWhoAmIHandler(t *testing.T) {
	t.Run("calls whoami", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := whoAmIHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
		}))
		require.NoError(t, err)

		require.Equal(t, http.MethodGet, env.last.Method, "method")
		require.Equal(t, "/ping/whoami", env.last.Path, "path")
	})
}

func TestRegisterAttachmentHandler(t *testing.T) {
	t.Run("sends the transaction as external_id", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := registerAttachmentHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"transaction_id": "tx_1",
			"file_url":       "https://example.com/receipt.png",
			"file_type":      "image/png",
		}))
		require.NoError(t, err)

		require.Equal(t, http.MethodPost, env.last.Method, "method")
		require.Equal(t, "/attachment/register", env.last.Path, "path")
		require.Equal(t, url.Values{
			"external_id": {"tx_1"},
			"file_url":    {"https://example.com/receipt.png"},
			"file_type":   {"image/png"},
		}, env.last.Form, "form body")
	})
}

func TestDeregisterAttachmentHandler(t *testing.T) {
	t.Run("sends the attachment id", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := deregisterAttachmentHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"attachment_id":  "attach_1",
		}))
		require.NoError(t, err)

		require.Equal(t, http.MethodPost, env.last.Method, "method")
		require.Equal(t, "/attachment/deregister", env.last.Path, "path")
		require.Equal(t, url.Values{"id": {"attach_1"}}, env.last.Form, "form body")
	})
}

func TestListWebhooksHandler(t *testing.T) {
	t.Run("lists by account", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := listWebhooksHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"account_id":     "acc_main",
		}))
		require.NoError(t, err)

		require.Equal(t, http.MethodGet, env.last.Method, "method")
		require.Equal(t, "/webhooks", env.last.Path, "path")
		require.Equal(t, "acc_main", env.last.Query.Get("account_id"), "account sent to Monzo")
	})
}

func TestRegisterWebhookHandler(t *testing.T) {
	t.Run("sends the account and url", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := registerWebhookHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"account_id":     "acc_main",
			"url":            "https://example.com/hook",
		}))
		require.NoError(t, err)

		require.Equal(t, http.MethodPost, env.last.Method, "method")
		require.Equal(t, "/webhooks", env.last.Path, "path")
		require.Equal(t, url.Values{"account_id": {"acc_main"}, "url": {"https://example.com/hook"}}, env.last.Form, "form body")
	})
}

func TestDeleteWebhookHandler(t *testing.T) {
	t.Run("deletes by id", func(t *testing.T) {
		env := setupMonzo(t)

		_, err := deleteWebhookHandler(env.depsMap)(context.Background(), mustJSON(t, map[string]any{
			"credential_set": string(env.setID),
			"webhook_id":     "webhook_1",
		}))
		require.NoError(t, err)

		require.Equal(t, http.MethodDelete, env.last.Method, "method")
		require.Equal(t, "/webhooks/webhook_1", env.last.Path, "path")
	})
}

func TestPackage_CredentialTools(t *testing.T) {
	t.Run("grants exactly the read tools", func(t *testing.T) {
		require.ElementsMatch(t, expectedReadTools(), (&Package{}).CredentialTools(), "credential tools")
	})
}

func TestPackage_GroupTools(t *testing.T) {
	t.Run("personal_services gets reads, monzo_write gets everything", func(t *testing.T) {
		groups := (&Package{}).GroupTools()

		require.ElementsMatch(t, expectedReadTools(), groups[toolgroup.GroupPersonalServices], "personal_services")
		require.Equal(t, []claudecli.Tool{toolgroup.MCPToolMonzoAll}, groups[toolgroup.GroupMonzoWrite], "monzo_write")
	})
}

type capturedRequest struct {
	Method      string
	Path        string
	Query       url.Values
	ContentType string
	Body        string
	Form        url.Values
}

type monzoEnv struct {
	setID   credential.CredentialSetID
	depsMap map[credential.CredentialSetID]Deps

	// status and rspBody are what the fake server answers with.
	status  int
	rspBody string

	last capturedRequest
}

// setupMonzo points the package at a fake Monzo server that records the last request.
func setupMonzo(t *testing.T) *monzoEnv {
	t.Helper()
	ctx := context.Background()

	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	mgr := credential.NewManager(s, &memorySecretStore{data: map[string]string{}})
	set, err := mgr.Add(ctx, credential.AddParams{Package: "monzo", Label: "test"})
	require.NoError(t, err)
	require.NoError(t, mgr.SetOAuthTokens(ctx, set.ID, &credential.OAuthTokens{AccessToken: "test-token"}))

	env := &monzoEnv{
		setID:   set.ID,
		depsMap: map[credential.CredentialSetID]Deps{set.ID: {CredSetID: set.ID, Manager: mgr}},
		status:  http.StatusOK,
		rspBody: `{"id":"pot_1"}`,
	}

	// The handler runs on the server's goroutine, where require cannot stop the test, so it uses assert.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"), "auth header")
		env.last = capturedRequest{
			Method:      r.Method,
			Path:        r.URL.Path,
			Query:       r.URL.Query(),
			ContentType: r.Header.Get("Content-Type"),
			Body:        string(body),
		}
		if env.last.ContentType == "application/x-www-form-urlencoded" {
			env.last.Form, err = url.ParseQuery(string(body))
			assert.NoError(t, err)
		}
		w.WriteHeader(env.status)
		_, err = w.Write([]byte(env.rspBody))
		assert.NoError(t, err)
	}))
	t.Cleanup(srv.Close)

	oldBaseURL := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { baseURL = oldBaseURL })

	return env
}

// expectedReadTools is written out by hand so a write tool slipping into the read list fails the tests.
func expectedReadTools() []claudecli.Tool {
	return []claudecli.Tool{
		"mcp__tclaw__monzo_whoami",
		"mcp__tclaw__monzo_list_accounts",
		"mcp__tclaw__monzo_get_balance",
		"mcp__tclaw__monzo_list_pots",
		"mcp__tclaw__monzo_list_transactions",
		"mcp__tclaw__monzo_get_transaction",
		"mcp__tclaw__monzo_get_receipt",
		"mcp__tclaw__monzo_list_webhooks",
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
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
