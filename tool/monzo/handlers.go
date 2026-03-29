package monzo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"tclaw/credential"
	"tclaw/libraries/credentialerror"
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

		// Resolve from params, falling back to secret store.
		clientID := a.ClientID
		if clientID == "" {
			stored, err := deps.SecretStore.Get(ctx, ClientIDStoreKey)
			if err != nil {
				return nil, fmt.Errorf("read stored client ID: %w", err)
			}
			clientID = stored
		}
		clientSecret := a.ClientSecret
		if clientSecret == "" {
			stored, err := deps.SecretStore.Get(ctx, ClientSecretStoreKey)
			if err != nil {
				return nil, fmt.Errorf("read stored client secret: %w", err)
			}
			clientSecret = stored
		}

		if clientID == "" || clientSecret == "" {
			return nil, credentialerror.New(
				"Monzo Configuration",
				"Create an API client at developers.monzo.com (personal use only). Set the redirect URI to your tclaw callback URL.",
				credentialerror.Field{Key: ClientIDStoreKey, Label: "Client ID", Description: "Monzo OAuth client ID from developers.monzo.com."},
				credentialerror.Field{Key: ClientSecretStoreKey, Label: "Client Secret", Description: "Monzo OAuth client secret from developers.monzo.com."},
			)
		}

		if err := deps.SecretStore.Set(ctx, ClientIDStoreKey, clientID); err != nil {
			return nil, fmt.Errorf("store client ID: %w", err)
		}
		if err := deps.SecretStore.Set(ctx, ClientSecretStoreKey, clientSecret); err != nil {
			return nil, fmt.Errorf("store client secret: %w", err)
		}

		if deps.OnCredentialsStored != nil {
			deps.OnCredentialsStored()
		}

		result := map[string]string{
			"status":  "stored",
			"message": "Monzo credentials saved. Use connection_add with provider 'monzo' to start the OAuth flow.",
		}
		if deps.RedirectURL != "" {
			result["redirect_url"] = deps.RedirectURL
			result["message"] += fmt.Sprintf(" Set %s as the redirect URI in your Monzo developer portal.", deps.RedirectURL)
		}
		return json.Marshal(result)
	}
}

type connectionArgs struct {
	CredentialSet string `json:"credential_set"`
}

func listAccountsHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			connectionArgs
			AccountType string `json:"account_type"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
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

func getBalanceHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
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
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}

		query := url.Values{"current_account_id": {p.AccountID}}
		return apiGet(ctx, deps, "/pots", query)
	}
}

func listTransactionsHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
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
		deps, err := resolveDeps(depsMap, p.CredentialSet)
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

func getTransactionHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
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
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}

		// Expand merchant details for richer transaction info.
		query := url.Values{"expand[]": {"merchant"}}
		return apiGet(ctx, deps, "/transactions/"+p.TransactionID, query)
	}
}
