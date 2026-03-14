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
