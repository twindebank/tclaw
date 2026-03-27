package monzo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"tclaw/connection"
	"tclaw/mcp"
)

func setCredentialsHandler(deps SetCredentialsDeps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}

		if a.ClientID == "" {
			return nil, fmt.Errorf("client_id is required")
		}
		if a.ClientSecret == "" {
			return nil, fmt.Errorf("client_secret is required")
		}

		if err := deps.SecretStore.Set(ctx, ClientIDStoreKey, a.ClientID); err != nil {
			return nil, fmt.Errorf("store client ID: %w", err)
		}
		if err := deps.SecretStore.Set(ctx, ClientSecretStoreKey, a.ClientSecret); err != nil {
			return nil, fmt.Errorf("store client secret: %w", err)
		}

		if deps.OnCredentialsStored != nil {
			deps.OnCredentialsStored()
		}

		return json.Marshal(map[string]string{
			"status":  "stored",
			"message": "Monzo credentials saved. Use connection_add with provider 'monzo' to start the OAuth flow.",
		})
	}
}

type connectionArgs struct {
	Connection string `json:"connection"`
}

func listAccountsHandler(connMap map[connection.ConnectionID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			connectionArgs
			AccountType string `json:"account_type"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		deps, err := resolveDeps(connMap, p.Connection)
		if err != nil {
			return nil, err
		}

		query := url.Values{}
		if p.AccountType != "" {
			query.Set("account_type", p.AccountType)
		}

		return apiGet(ctx, deps, "/accounts", query)
	}
}

func getBalanceHandler(connMap map[connection.ConnectionID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			connectionArgs
			AccountID string `json:"account_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if p.AccountID == "" {
			return nil, fmt.Errorf("account_id is required")
		}
		deps, err := resolveDeps(connMap, p.Connection)
		if err != nil {
			return nil, err
		}

		query := url.Values{"account_id": {p.AccountID}}
		return apiGet(ctx, deps, "/balance", query)
	}
}

func listPotsHandler(connMap map[connection.ConnectionID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			connectionArgs
			AccountID string `json:"account_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if p.AccountID == "" {
			return nil, fmt.Errorf("account_id is required")
		}
		deps, err := resolveDeps(connMap, p.Connection)
		if err != nil {
			return nil, err
		}

		query := url.Values{"current_account_id": {p.AccountID}}
		return apiGet(ctx, deps, "/pots", query)
	}
}

func listTransactionsHandler(connMap map[connection.ConnectionID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			connectionArgs
			AccountID string `json:"account_id"`
			Since     string `json:"since"`
			Before    string `json:"before"`
			Limit     int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if p.AccountID == "" {
			return nil, fmt.Errorf("account_id is required")
		}
		deps, err := resolveDeps(connMap, p.Connection)
		if err != nil {
			return nil, err
		}

		query := url.Values{
			"account_id": {p.AccountID},
			"expand[]":   {"merchant"},
		}
		if p.Since != "" {
			query.Set("since", p.Since)
		}
		if p.Before != "" {
			query.Set("before", p.Before)
		}
		if p.Limit > 0 {
			if p.Limit > 100 {
				p.Limit = 100
			}
			query.Set("limit", strconv.Itoa(p.Limit))
		}

		return apiGet(ctx, deps, "/transactions", query)
	}
}

func getTransactionHandler(connMap map[connection.ConnectionID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			connectionArgs
			TransactionID string `json:"transaction_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if p.TransactionID == "" {
			return nil, fmt.Errorf("transaction_id is required")
		}
		deps, err := resolveDeps(connMap, p.Connection)
		if err != nil {
			return nil, err
		}

		// Expand merchant details for richer transaction info.
		query := url.Values{"expand[]": {"merchant"}}
		return apiGet(ctx, deps, "/transactions/"+p.TransactionID, query)
	}
}
