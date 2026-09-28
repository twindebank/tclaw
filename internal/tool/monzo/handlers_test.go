package monzo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/credential"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
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

	// status is what the fake server answers with.
	status int

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
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"), "auth header")
		env.last = capturedRequest{
			Method:      r.Method,
			Path:        r.URL.Path,
			Query:       r.URL.Query(),
			ContentType: r.Header.Get("Content-Type"),
			Body:        string(body),
		}
		if env.last.ContentType == "application/x-www-form-urlencoded" {
			env.last.Form, err = url.ParseQuery(string(body))
			require.NoError(t, err)
		}
		w.WriteHeader(env.status)
		_, err = w.Write([]byte(`{"id":"pot_1"}`))
		require.NoError(t, err)
	}))
	t.Cleanup(srv.Close)

	oldBaseURL := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { baseURL = oldBaseURL })

	return env
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
