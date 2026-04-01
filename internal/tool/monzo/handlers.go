package monzo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"tclaw/internal/credential"
	"tclaw/internal/mcp"
)

type connectionArgs struct {
	CredentialSet string `json:"credential_set"`
}

func listAccountsHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p connectionArgs
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		return apiGet(ctx, deps, "/accounts", nil)
	}
}

func getBalanceHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet string `json:"credential_set"`
			AccountID     string `json:"account_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		query := url.Values{"account_id": {p.AccountID}}
		return apiGet(ctx, deps, "/balance", query)
	}
}

func listPotsHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p connectionArgs
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		return apiGet(ctx, deps, "/pots", nil)
	}
}

func listTransactionsHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet string `json:"credential_set"`
			AccountID     string `json:"account_id"`
			Since         string `json:"since"`
			Limit         int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		query := url.Values{"account_id": {p.AccountID}}
		if p.Since != "" {
			query.Set("since", p.Since)
		}
		if p.Limit > 0 {
			query.Set("limit", strconv.Itoa(p.Limit))
		}
		return apiGet(ctx, deps, "/transactions", query)
	}
}

func getTransactionHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet string `json:"credential_set"`
			TransactionID string `json:"transaction_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		query := url.Values{"expand[]": {"merchant"}}
		return apiGet(ctx, deps, "/transactions/"+p.TransactionID, query)
	}
}
