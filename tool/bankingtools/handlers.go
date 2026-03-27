package bankingtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"tclaw/mcp"
)

const authWaitTimeout = 5 * time.Minute

// handlerState holds per-user state shared across all banking tool handlers.
// Created once per RegisterTools call, so each user gets their own instance.
type handlerState struct {
	deps     Deps
	sessions *SessionStore

	// pendingFlows tracks in-progress bank authorization flows by ASPSP name.
	pendingFlows sync.Map
}

func makeHandler(name string, state *handlerState) mcp.ToolHandler {
	switch name {
	case "banking_set_credentials":
		return setCredentialsHandler(state)
	case "banking_list_banks":
		return listBanksHandler(state)
	case "banking_connect":
		return connectHandler(state)
	case "banking_auth_wait":
		return authWaitHandler(state)
	case "banking_list_accounts":
		return listAccountsHandler(state)
	case "banking_get_balance":
		return getBalanceHandler(state)
	case "banking_get_transactions":
		return getTransactionsHandler(state)
	default:
		return func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("unknown banking tool: %s", name)
		}
	}
}

// buildClient reads credentials from the secret store and creates an API client.
func buildClient(ctx context.Context, s *handlerState) (*Client, error) {
	appID, err := s.deps.SecretStore.Get(ctx, ApplicationIDStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read application ID: %w", err)
	}
	if appID == "" {
		return nil, fmt.Errorf("Enable Banking credentials not configured — call banking_set_credentials first (register for free at enablebanking.com)")
	}

	privateKey, err := s.deps.SecretStore.Get(ctx, PrivateKeyStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	if privateKey == "" {
		return nil, fmt.Errorf("Enable Banking private key not configured — call banking_set_credentials with your PEM private key")
	}

	return NewClient(appID, privateKey)
}

func setCredentialsHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			ApplicationID string `json:"application_id"`
			PrivateKeyPEM string `json:"private_key_pem"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.ApplicationID == "" {
			return nil, fmt.Errorf("application_id is required")
		}
		if a.PrivateKeyPEM == "" {
			return nil, fmt.Errorf("private_key_pem is required")
		}

		// Validate the key parses before storing.
		if _, err := NewClient(a.ApplicationID, a.PrivateKeyPEM); err != nil {
			return nil, fmt.Errorf("invalid credentials: %w", err)
		}

		if err := s.deps.SecretStore.Set(ctx, ApplicationIDStoreKey, a.ApplicationID); err != nil {
			return nil, fmt.Errorf("store application ID: %w", err)
		}
		if err := s.deps.SecretStore.Set(ctx, PrivateKeyStoreKey, a.PrivateKeyPEM); err != nil {
			return nil, fmt.Errorf("store private key: %w", err)
		}

		if s.deps.OnCredentialsStored != nil {
			s.deps.OnCredentialsStored()
		}

		return json.Marshal(map[string]string{
			"status":  "stored",
			"message": "Enable Banking credentials saved. You can now use banking_list_banks and banking_connect.",
		})
	}
}

func listBanksHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			Country string `json:"country"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Country == "" {
			a.Country = "GB"
		}

		client, err := buildClient(ctx, s)
		if err != nil {
			return nil, err
		}

		return client.ListBanks(ctx, a.Country)
	}
}

func connectHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			ASPSPName    string `json:"aspsp_name"`
			ASPSPCountry string `json:"aspsp_country"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.ASPSPName == "" {
			return nil, fmt.Errorf("aspsp_name is required")
		}
		if a.ASPSPCountry == "" {
			a.ASPSPCountry = "GB"
		}

		if s.deps.Callback == nil {
			return nil, fmt.Errorf("OAuth callback server not available — bank authorization requires an HTTP callback endpoint")
		}

		client, err := buildClient(ctx, s)
		if err != nil {
			return nil, err
		}

		// Validate the ASPSP name against the actual bank list to catch typos
		// before hitting the API. On mismatch, return fuzzy matches so the
		// agent can self-correct.
		if err := validateASPSPName(ctx, client, a.ASPSPName, a.ASPSPCountry); err != nil {
			return nil, err
		}

		flow := NewBankingPendingFlow(client, s.sessions, a.ASPSPName, a.ASPSPName, a.ASPSPCountry)

		// Register the flow with the callback server to get a state token.
		state, err := s.deps.Callback.RegisterFlow(flow)
		if err != nil {
			return nil, fmt.Errorf("register auth flow: %w", err)
		}

		// 90-day session validity (standard PSD2 maximum).
		validUntil := time.Now().Add(90 * 24 * time.Hour)

		authResp, err := client.StartAuth(ctx, StartAuthParams{
			ASPSPName:    a.ASPSPName,
			ASPSPCountry: a.ASPSPCountry,
			RedirectURL:  s.deps.Callback.CallbackURL(),
			State:        state,
			ValidUntil:   validUntil,
		})
		if err != nil {
			flow.Fail(fmt.Errorf("start auth failed: %w", err))
			return nil, fmt.Errorf("start bank authorization: %w", err)
		}

		// Track the flow so banking_auth_wait can find it.
		s.pendingFlows.Store(a.ASPSPName, flow)

		return json.Marshal(map[string]string{
			"status":       "pending",
			"url":          authResp.URL,
			"redirect_url": s.deps.Callback.CallbackURL(),
			"message":      fmt.Sprintf("Send this authorization URL to the user. After they complete bank login at their bank, they'll be redirected to %s. Then call banking_auth_wait with aspsp_name=%q.", s.deps.Callback.CallbackURL(), a.ASPSPName),
		})
	}
}

func authWaitHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			ASPSPName string `json:"aspsp_name"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.ASPSPName == "" {
			return nil, fmt.Errorf("aspsp_name is required")
		}

		val, ok := s.pendingFlows.Load(a.ASPSPName)
		if !ok {
			return nil, fmt.Errorf("no pending authorization for %q — call banking_connect first", a.ASPSPName)
		}
		flow := val.(*BankingPendingFlow)

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("authorization wait cancelled")
		case <-time.After(authWaitTimeout):
			s.pendingFlows.Delete(a.ASPSPName)
			return nil, fmt.Errorf("authorization timed out for %q — the user may not have completed bank login, try banking_connect again", a.ASPSPName)
		case <-flow.DoneChan():
			s.pendingFlows.Delete(a.ASPSPName)

			if flow.Err != nil {
				return nil, fmt.Errorf("bank authorization failed: %w", flow.Err)
			}

			accounts := make([]map[string]string, len(flow.Result.AccountIDs))
			for i, uid := range flow.Result.AccountIDs {
				accounts[i] = map[string]string{"account_id": uid}
			}

			result := map[string]any{
				"aspsp_name":  a.ASPSPName,
				"status":      "authorized",
				"session_id":  flow.Result.SessionID,
				"accounts":    accounts,
				"valid_until": flow.Result.ValidUntil.Format(time.RFC3339),
				"message":     fmt.Sprintf("Bank %q connected with %d account(s). Use banking_list_accounts to see details.", a.ASPSPName, len(flow.Result.AccountIDs)),
			}
			return json.Marshal(result)
		}
	}
}

func listAccountsHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		allSessions, err := s.sessions.List(ctx)
		if err != nil {
			return nil, err
		}
		if len(allSessions) == 0 {
			return json.Marshal(map[string]any{
				"accounts": []any{},
				"message":  "No banks connected. Use banking_connect to link a bank account.",
			})
		}

		type accountInfo struct {
			AccountID string `json:"account_id"`
			BankName  string `json:"bank_name"`
			Expired   bool   `json:"expired,omitempty"`
		}

		var accounts []accountInfo
		var expiredBanks []string

		for _, sess := range allSessions {
			expired := sess.IsExpired()
			if expired {
				expiredBanks = append(expiredBanks, sess.BankName)
			}
			for _, uid := range sess.AccountIDs {
				accounts = append(accounts, accountInfo{
					AccountID: uid,
					BankName:  sess.BankName,
					Expired:   expired,
				})
			}
		}

		result := map[string]any{
			"accounts": accounts,
		}
		if len(expiredBanks) > 0 {
			result["expired_banks"] = expiredBanks
			result["message"] = "Some bank sessions have expired. Reconnect them with banking_connect."
		}
		return json.Marshal(result)
	}
}

func getBalanceHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			AccountID string `json:"account_id"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.AccountID == "" {
			return nil, fmt.Errorf("account_id is required")
		}

		sess, err := s.sessions.FindByAccountID(ctx, a.AccountID)
		if err != nil {
			return nil, err
		}
		if sess.IsExpired() {
			return nil, fmt.Errorf("session for %s expired on %s — reconnect with banking_connect using aspsp_name=%q", sess.BankName, sess.ValidUntil.Format("2006-01-02"), sess.BankName)
		}

		client, err := buildClient(ctx, s)
		if err != nil {
			return nil, err
		}

		return client.GetBalances(ctx, a.AccountID)
	}
}

func getTransactionsHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			AccountID       string `json:"account_id"`
			DateFrom        string `json:"date_from"`
			DateTo          string `json:"date_to"`
			ContinuationKey string `json:"continuation_key"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.AccountID == "" {
			return nil, fmt.Errorf("account_id is required")
		}

		sess, err := s.sessions.FindByAccountID(ctx, a.AccountID)
		if err != nil {
			return nil, err
		}
		if sess.IsExpired() {
			return nil, fmt.Errorf("session for %s expired on %s — reconnect with banking_connect using aspsp_name=%q", sess.BankName, sess.ValidUntil.Format("2006-01-02"), sess.BankName)
		}

		client, err := buildClient(ctx, s)
		if err != nil {
			return nil, err
		}

		return client.GetTransactions(ctx, a.AccountID, TransactionParams{
			DateFrom:        a.DateFrom,
			DateTo:          a.DateTo,
			ContinuationKey: a.ContinuationKey,
		})
	}
}

// validateASPSPName fetches the bank list and checks that the given name
// matches exactly. On mismatch, returns an error with fuzzy-matched
// suggestions so the agent can self-correct.
func validateASPSPName(ctx context.Context, client *Client, name string, country string) error {
	raw, err := client.ListBanks(ctx, country)
	if err != nil {
		// Can't validate — let the API return its own error.
		return nil
	}

	var banks []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &banks); err != nil {
		// Unexpected response shape — skip validation.
		return nil
	}

	nameLower := strings.ToLower(name)
	for _, b := range banks {
		if b.Name == name {
			return nil
		}
	}

	// No exact match — find fuzzy matches (case-insensitive substring).
	var matches []string
	for _, b := range banks {
		if strings.Contains(strings.ToLower(b.Name), nameLower) || strings.Contains(nameLower, strings.ToLower(b.Name)) {
			matches = append(matches, b.Name)
		}
	}

	if len(matches) > 10 {
		matches = matches[:10]
	}

	if len(matches) > 0 {
		return fmt.Errorf("bank %q not found — did you mean one of these? %s (names must match exactly as returned by banking_list_banks)", name, strings.Join(matches, ", "))
	}

	return fmt.Errorf("bank %q not found in %s — call banking_list_banks to see available banks (names must match exactly)", name, country)
}
