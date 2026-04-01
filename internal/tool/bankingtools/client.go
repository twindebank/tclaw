package bankingtools

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const baseURL = "https://api.enablebanking.com"

// Client is an Enable Banking API client that authenticates via JWT Bearer tokens.
type Client struct {
	applicationID string
	privateKey    *rsa.PrivateKey
	httpClient    *http.Client
}

// NewClient creates a client from an application ID and PEM-encoded RSA private key.
func NewClient(applicationID string, privateKeyPEM string) (*Client, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block — ensure the private key is in PEM format")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Fall back to PKCS1 format.
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key (tried PKCS8 and PKCS1): %w", err)
		}
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA (got %T)", key)
	}

	return &Client{
		applicationID: applicationID,
		privateKey:    rsaKey,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// generateJWT creates an RS256-signed JWT for Enable Banking API authentication.
func (c *Client) generateJWT() (string, error) {
	header, err := json.Marshal(map[string]string{
		"alg": "RS256",
		"typ": "JWT",
		"kid": c.applicationID,
	})
	if err != nil {
		return "", fmt.Errorf("marshal JWT header: %w", err)
	}

	now := time.Now()
	claims, err := json.Marshal(map[string]any{
		"iss": "enablebanking.com",
		"aud": "api.enablebanking.com",
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("marshal JWT claims: %w", err)
	}

	signingInput := base64URLEncode(header) + "." + base64URLEncode(claims)

	hash := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	return signingInput + "." + base64URLEncode(signature), nil
}

func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// StartAuthParams are the parameters for starting bank authorization.
type StartAuthParams struct {
	ASPSPName    string
	ASPSPCountry string
	RedirectURL  string
	State        string
	ValidUntil   time.Time
}

// StartAuthResponse is the response from POST /auth.
type StartAuthResponse struct {
	URL             string `json:"url"`
	AuthorizationID string `json:"authorization_id"`
}

// StartAuth initiates a bank authorization flow.
func (c *Client) StartAuth(ctx context.Context, params StartAuthParams) (*StartAuthResponse, error) {
	body := map[string]any{
		"access": map[string]any{
			"valid_until": params.ValidUntil.UTC().Format(time.RFC3339),
		},
		"aspsp": map[string]any{
			"name":    params.ASPSPName,
			"country": params.ASPSPCountry,
		},
		"state":        params.State,
		"redirect_url": params.RedirectURL,
		"psu_type":     "personal",
	}

	var resp StartAuthResponse
	if err := c.doJSON(ctx, http.MethodPost, "/auth", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SessionAccount is an account returned when creating a session.
type SessionAccount struct {
	UID             string `json:"uid"`
	Name            string `json:"name"`
	Currency        string `json:"currency"`
	CashAccountType string `json:"cash_account_type"`
	AccountID       struct {
		IBAN string `json:"iban"`
	} `json:"account_id"`
}

// CreateSessionResponse is the response from POST /sessions.
type CreateSessionResponse struct {
	SessionID string           `json:"session_id"`
	Accounts  []SessionAccount `json:"accounts"`
	ASPSP     struct {
		Name    string `json:"name"`
		Country string `json:"country"`
	} `json:"aspsp"`
	Access struct {
		ValidUntil time.Time `json:"valid_until"`
	} `json:"access"`
}

// CreateSession exchanges an authorization code for a session with account access.
func (c *Client) CreateSession(ctx context.Context, code string) (*CreateSessionResponse, error) {
	body := map[string]any{"code": code}

	var resp CreateSessionResponse
	if err := c.doJSON(ctx, http.MethodPost, "/sessions", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListBanks returns available financial institutions for a country.
func (c *Client) ListBanks(ctx context.Context, country string) (json.RawMessage, error) {
	query := url.Values{}
	if country != "" {
		query.Set("country", country)
	}
	return c.doGet(ctx, "/aspsps", query)
}

// GetBalances returns balances for an account.
func (c *Client) GetBalances(ctx context.Context, accountID string) (json.RawMessage, error) {
	return c.doGet(ctx, "/accounts/"+accountID+"/balances", nil)
}

// TransactionParams are optional filters for fetching transactions.
type TransactionParams struct {
	DateFrom        string // YYYY-MM-DD
	DateTo          string // YYYY-MM-DD
	ContinuationKey string
}

// GetTransactions returns transactions for an account with optional date filtering.
func (c *Client) GetTransactions(ctx context.Context, accountID string, params TransactionParams) (json.RawMessage, error) {
	query := url.Values{}
	if params.DateFrom != "" {
		query.Set("date_from", params.DateFrom)
	}
	if params.DateTo != "" {
		query.Set("date_to", params.DateTo)
	}
	if params.ContinuationKey != "" {
		query.Set("continuation_key", params.ContinuationKey)
	}
	return c.doGet(ctx, "/accounts/"+accountID+"/transactions", query)
}

// doGet performs an authenticated GET request and returns the raw response body.
func (c *Client) doGet(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	reqURL := baseURL + path
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	return c.doRequest(req)
}

// doJSON performs an authenticated request with a JSON body and decodes the response.
func (c *Client) doJSON(ctx context.Context, method string, path string, body any, dest any) error {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	respBody, err := c.doRequest(req)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(respBody, dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// doRequest signs a request with a JWT and executes it.
func (c *Client) doRequest(req *http.Request) (json.RawMessage, error) {
	token, err := c.generateJWT()
	if err != nil {
		return nil, fmt.Errorf("generate JWT: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Enable Banking API %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Enable Banking API %s %s returned %d: %s", req.Method, req.URL.Path, resp.StatusCode, string(body))
	}

	return json.RawMessage(body), nil
}
