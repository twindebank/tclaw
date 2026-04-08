package monzo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

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
		} else {
			// Default to 30 days. Monzo's SCA blocks access beyond 90 days without in-app verification.
			query.Set("since", time.Now().AddDate(0, 0, -30).UTC().Format(time.RFC3339))
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
		return apiGet(ctx, deps, "/transactions/"+url.PathEscape(p.TransactionID), query)
	}
}
