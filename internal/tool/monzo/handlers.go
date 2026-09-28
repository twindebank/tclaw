package monzo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"tclaw/internal/credential"
	"tclaw/internal/mcp"
)

type connectionArgs struct {
	CredentialSet string `json:"credential_set"`
}

func whoAmIHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p connectionArgs
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		return apiRequest(ctx, deps, apiRequestParams{Method: http.MethodGet, Path: "/ping/whoami"})
	}
}

func listAccountsHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet string `json:"credential_set"`
			AccountType   string `json:"account_type"`
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
		return apiRequest(ctx, deps, apiRequestParams{Method: http.MethodGet, Path: "/accounts", Query: query})
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
		return apiRequest(ctx, deps, apiRequestParams{
			Method: http.MethodGet,
			Path:   "/balance",
			Query:  url.Values{"account_id": {p.AccountID}},
		})
	}
}

func listPotsHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
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
		// Monzo only returns the pots belonging to this account, so a joint account's pots need its own ID.
		return apiRequest(ctx, deps, apiRequestParams{
			Method: http.MethodGet,
			Path:   "/pots",
			Query:  url.Values{"current_account_id": {p.AccountID}},
		})
	}
}

func listTransactionsHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet string `json:"credential_set"`
			AccountID     string `json:"account_id"`
			Since         string `json:"since"`
			Before        string `json:"before"`
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
		if p.Before != "" {
			query.Set("before", p.Before)
		}
		if p.Limit > 0 {
			query.Set("limit", strconv.Itoa(p.Limit))
		}
		return apiRequest(ctx, deps, apiRequestParams{Method: http.MethodGet, Path: "/transactions", Query: query})
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
		return apiRequest(ctx, deps, apiRequestParams{
			Method: http.MethodGet,
			Path:   "/transactions/" + url.PathEscape(p.TransactionID),
			Query:  url.Values{"expand[]": {"merchant"}},
		})
	}
}

func annotateTransactionHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet string            `json:"credential_set"`
			TransactionID string            `json:"transaction_id"`
			Metadata      map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if p.TransactionID == "" || len(p.Metadata) == 0 {
			return nil, errors.New("transaction_id and at least one metadata key are required")
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		form := url.Values{}
		for k, v := range p.Metadata {
			form.Set("metadata["+k+"]", v)
		}
		return apiRequest(ctx, deps, apiRequestParams{
			Method: http.MethodPatch,
			Path:   "/transactions/" + url.PathEscape(p.TransactionID),
			Form:   form,
		})
	}
}

func createFeedItemHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet   string `json:"credential_set"`
			AccountID       string `json:"account_id"`
			Title           string `json:"title"`
			ImageURL        string `json:"image_url"`
			Body            string `json:"body"`
			URL             string `json:"url"`
			BackgroundColor string `json:"background_color"`
			TitleColor      string `json:"title_color"`
			BodyColor       string `json:"body_color"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if p.AccountID == "" || p.Title == "" || p.ImageURL == "" {
			return nil, errors.New("account_id, title and image_url are required")
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		form := url.Values{
			"account_id":        {p.AccountID},
			"type":              {"basic"},
			"params[title]":     {p.Title},
			"params[image_url]": {p.ImageURL},
		}
		optional := map[string]string{
			"url":                      p.URL,
			"params[body]":             p.Body,
			"params[background_color]": p.BackgroundColor,
			"params[title_color]":      p.TitleColor,
			"params[body_color]":       p.BodyColor,
		}
		for k, v := range optional {
			if v != "" {
				form.Set(k, v)
			}
		}
		return apiRequest(ctx, deps, apiRequestParams{Method: http.MethodPost, Path: "/feed", Form: form})
	}
}

func registerAttachmentHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet string `json:"credential_set"`
			TransactionID string `json:"transaction_id"`
			FileURL       string `json:"file_url"`
			FileType      string `json:"file_type"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if p.TransactionID == "" || p.FileURL == "" || p.FileType == "" {
			return nil, errors.New("transaction_id, file_url and file_type are required")
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		return apiRequest(ctx, deps, apiRequestParams{
			Method: http.MethodPost,
			Path:   "/attachment/register",
			Form: url.Values{
				"external_id": {p.TransactionID},
				"file_url":    {p.FileURL},
				"file_type":   {p.FileType},
			},
		})
	}
}

func deregisterAttachmentHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet string `json:"credential_set"`
			AttachmentID  string `json:"attachment_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if p.AttachmentID == "" {
			return nil, errors.New("attachment_id is required")
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		return apiRequest(ctx, deps, apiRequestParams{
			Method: http.MethodPost,
			Path:   "/attachment/deregister",
			Form:   url.Values{"id": {p.AttachmentID}},
		})
	}
}

// receiptByExternalIDHandler fetches or deletes the receipt with the given external ID, depending on method.
func receiptByExternalIDHandler(depsMap map[credential.CredentialSetID]Deps, method string) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet string `json:"credential_set"`
			ExternalID    string `json:"external_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if p.ExternalID == "" {
			return nil, errors.New("external_id is required")
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		return apiRequest(ctx, deps, apiRequestParams{
			Method: method,
			Path:   "/transaction-receipts",
			Query:  url.Values{"external_id": {p.ExternalID}},
		})
	}
}

func setReceiptHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet string          `json:"credential_set"`
			Receipt       json.RawMessage `json:"receipt"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if len(p.Receipt) == 0 {
			return nil, errors.New("receipt is required")
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		return apiRequest(ctx, deps, apiRequestParams{
			Method: http.MethodPut,
			Path:   "/transaction-receipts",
			JSON:   p.Receipt,
		})
	}
}

func listWebhooksHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
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
		return apiRequest(ctx, deps, apiRequestParams{
			Method: http.MethodGet,
			Path:   "/webhooks",
			Query:  url.Values{"account_id": {p.AccountID}},
		})
	}
}

func registerWebhookHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet string `json:"credential_set"`
			AccountID     string `json:"account_id"`
			URL           string `json:"url"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if p.AccountID == "" || p.URL == "" {
			return nil, errors.New("account_id and url are required")
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		return apiRequest(ctx, deps, apiRequestParams{
			Method: http.MethodPost,
			Path:   "/webhooks",
			Form:   url.Values{"account_id": {p.AccountID}, "url": {p.URL}},
		})
	}
}

func deleteWebhookHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p struct {
			CredentialSet string `json:"credential_set"`
			WebhookID     string `json:"webhook_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if p.WebhookID == "" {
			return nil, errors.New("webhook_id is required")
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}
		return apiRequest(ctx, deps, apiRequestParams{
			Method: http.MethodDelete,
			Path:   "/webhooks/" + url.PathEscape(p.WebhookID),
		})
	}
}

// potTransfer is the direction money moves between a pot and its account.
type potTransfer string

const (
	potDeposit  potTransfer = "deposit"
	potWithdraw potTransfer = "withdraw"
)

type potTransferArgs struct {
	CredentialSet string `json:"credential_set"`
	PotID         string `json:"pot_id"`
	AccountID     string `json:"account_id"`
	Amount        int64  `json:"amount"`
	DedupeID      string `json:"dedupe_id"`
}

func potTransferHandler(depsMap map[credential.CredentialSetID]Deps, direction potTransfer) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p potTransferArgs
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse args: %w", err)
		}
		if err := validatePotTransfer(p); err != nil {
			return nil, err
		}
		deps, err := resolveDeps(depsMap, p.CredentialSet)
		if err != nil {
			return nil, err
		}

		var accountField string
		switch direction {
		case potDeposit:
			accountField = "source_account_id"
		case potWithdraw:
			accountField = "destination_account_id"
		default:
			return nil, fmt.Errorf("unknown pot transfer direction %q", direction)
		}

		return apiRequest(ctx, deps, apiRequestParams{
			Method: http.MethodPut,
			Path:   "/pots/" + url.PathEscape(p.PotID) + "/" + string(direction),
			Form: url.Values{
				accountField: {p.AccountID},
				"amount":     {strconv.FormatInt(p.Amount, 10)},
				"dedupe_id":  {p.DedupeID},
			},
		})
	}
}

func validatePotTransfer(p potTransferArgs) error {
	var errs []error
	if p.PotID == "" {
		errs = append(errs, errors.New("pot_id is required"))
	}
	if p.AccountID == "" {
		errs = append(errs, errors.New("account_id is required"))
	}
	if p.Amount <= 0 {
		errs = append(errs, fmt.Errorf("amount must be a positive number of pence, got %d", p.Amount))
	}
	if p.DedupeID == "" {
		errs = append(errs, errors.New("dedupe_id is required"))
	}
	return errors.Join(errs...)
}
