package restauranttools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"strings"
	"tclaw/libraries/credentialerror"
	"time"

	"tclaw/libraries/secret"
)

const (
	resyBaseURL = "https://api.resy.com"

	// ResyAPIKeyStoreKey is the secret store key for the Resy API key.
	ResyAPIKeyStoreKey = "resy_api_key"

	// ResyAuthTokenStoreKey is the secret store key for the Resy auth token.
	ResyAuthTokenStoreKey = "resy_auth_token"
)

// ResyProvider implements Provider for the Resy restaurant platform.
type ResyProvider struct {
	secretStore secret.Store
}

// NewResyProvider creates a Resy provider backed by the given secret store.
func NewResyProvider(secretStore secret.Store) *ResyProvider {
	return &ResyProvider{secretStore: secretStore}
}

func (r *ResyProvider) Name() string { return "resy" }

func (r *ResyProvider) Search(ctx context.Context, params SearchParams) (json.RawMessage, error) {
	query := url.Values{}
	query.Set("day", params.Day)

	partySize := params.PartySize
	if partySize == 0 {
		partySize = 2
	}
	query.Set("party_size", fmt.Sprintf("%d", partySize))

	lat := params.Lat
	long := params.Long
	query.Set("lat", fmt.Sprintf("%f", lat))
	query.Set("long", fmt.Sprintf("%f", long))

	if params.Query != "" {
		query.Set("query", params.Query)
	}

	return r.apiGet(ctx, "/4/find", query)
}

func (r *ResyProvider) Availability(ctx context.Context, params AvailabilityParams) (json.RawMessage, error) {
	query := url.Values{}
	query.Set("venue_id", params.VenueID)
	query.Set("day", params.Day)

	partySize := params.PartySize
	if partySize == 0 {
		partySize = 2
	}
	query.Set("party_size", fmt.Sprintf("%d", partySize))

	// Use lat=0&long=0 since we're querying a specific venue.
	query.Set("lat", "0")
	query.Set("long", "0")

	return r.apiGet(ctx, "/4/find", query)
}

func (r *ResyProvider) Book(ctx context.Context, params BookParams) (json.RawMessage, error) {
	// Step 1: Get the book_token from the details endpoint.
	detailsQuery := url.Values{}
	detailsQuery.Set("config_id", params.ConfigID)
	detailsQuery.Set("day", params.Day)
	detailsQuery.Set("party_size", fmt.Sprintf("%d", params.PartySize))

	detailsBody, err := r.apiGet(ctx, "/3/details", detailsQuery)
	if err != nil {
		return nil, fmt.Errorf("get booking details: %w", err)
	}

	// Extract the book_token from the details response.
	var details struct {
		BookToken struct {
			Value string `json:"value"`
		} `json:"book_token"`
	}
	if err := json.Unmarshal(detailsBody, &details); err != nil {
		return nil, fmt.Errorf("parse details response: %w", err)
	}
	if details.BookToken.Value == "" {
		return nil, fmt.Errorf("no book_token in details response — the slot may no longer be available")
	}

	// Step 2: Submit the booking.
	paymentMethod, err := json.Marshal(map[string]int{"id": params.PaymentMethodID})
	if err != nil {
		return nil, fmt.Errorf("marshal payment method: %w", err)
	}

	formData := url.Values{}
	formData.Set("book_token", details.BookToken.Value)
	formData.Set("struct_payment_method", string(paymentMethod))

	return r.apiPost(ctx, "/3/book", formData)
}

func (r *ResyProvider) CancelBooking(_ context.Context, _ CancelParams) (json.RawMessage, error) {
	// Resy cancel endpoint not yet discovered — will be added when the
	// API endpoint is identified via browser network inspection.
	return nil, ErrNotSupported
}

func (r *ResyProvider) ListBookings(_ context.Context) (json.RawMessage, error) {
	// Resy list bookings endpoint not yet discovered — will be added when
	// the API endpoint is identified via browser network inspection.
	return nil, ErrNotSupported
}

func (r *ResyProvider) PersistCredentials(ctx context.Context, credentials map[string]string) error {
	apiKey := credentials["api_key"]
	authToken := credentials["auth_token"]

	if apiKey == "" || authToken == "" {
		return fmt.Errorf("both api_key and auth_token are required for resy")
	}

	if err := r.secretStore.Set(ctx, ResyAPIKeyStoreKey, apiKey); err != nil {
		return fmt.Errorf("store api_key: %w", err)
	}
	if err := r.secretStore.Set(ctx, ResyAuthTokenStoreKey, authToken); err != nil {
		return fmt.Errorf("store auth_token: %w", err)
	}
	return nil
}

// readCredentials loads the Resy API key and auth token from the secret store.
func (r *ResyProvider) readCredentials(ctx context.Context) (apiKey string, authToken string, err error) {
	apiKey, err = r.secretStore.Get(ctx, ResyAPIKeyStoreKey)
	if err != nil {
		slog.Debug("failed to read resy api key from store", "err", err)
	}
	if apiKey == "" {
		return "", "", credentialerror.New(
			"Resy Configuration",
			"Get your credentials from browser dev tools on resy.com — open Network tab, find any API request, copy the Authorization header value (after 'ResyAPI api_key=') and the x-resy-auth-token header value.",
			credentialerror.Field{Key: ResyAPIKeyStoreKey, Label: "Resy API Key", Description: "Authorization header value (after 'ResyAPI api_key=')"},
			credentialerror.Field{Key: ResyAuthTokenStoreKey, Label: "Resy Auth Token", Description: "x-resy-auth-token header value"},
		)
	}

	authToken, err = r.secretStore.Get(ctx, ResyAuthTokenStoreKey)
	if err != nil {
		slog.Debug("failed to read resy auth token from store", "err", err)
	}
	if authToken == "" {
		return "", "", credentialerror.New(
			"Resy Configuration",
			"Your Resy auth token is missing.",
			credentialerror.Field{Key: ResyAuthTokenStoreKey, Label: "Resy Auth Token", Description: "x-resy-auth-token header value from resy.com"},
		)
	}

	return apiKey, authToken, nil
}

// apiGet makes an authenticated GET request to the Resy API.
func (r *ResyProvider) apiGet(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	apiKey, authToken, err := r.readCredentials(ctx)
	if err != nil {
		return nil, err
	}

	reqURL := resyBaseURL + path
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	r.setHeaders(req, apiKey, authToken)

	return r.doRequest(req, path)
}

// apiPost makes an authenticated POST request to the Resy API with form-urlencoded body.
func (r *ResyProvider) apiPost(ctx context.Context, path string, formData url.Values) (json.RawMessage, error) {
	apiKey, authToken, err := r.readCredentials(ctx)
	if err != nil {
		return nil, err
	}

	reqURL := resyBaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.setHeaders(req, apiKey, authToken)

	return r.doRequest(req, path)
}

func (r *ResyProvider) setHeaders(req *http.Request, apiKey, authToken string) {
	req.Header.Set("Authorization", fmt.Sprintf(`ResyAPI api_key="%s"`, apiKey))
	req.Header.Set("X-Resy-Auth-Token", authToken)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; tclaw/1.0; +https://github.com/twindebank/tclaw)")
	req.Header.Set("Origin", "https://widgets.resy.com")
	req.Header.Set("Referer", "https://widgets.resy.com/")
}

func (r *ResyProvider) doRequest(req *http.Request, path string) (json.RawMessage, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("resy API %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, credentialerror.New(
			"Resy Credentials Expired",
			fmt.Sprintf("Resy returned HTTP %d — your credentials are invalid or expired. Get fresh values from browser dev tools on resy.com.", resp.StatusCode),
			credentialerror.Field{Key: ResyAPIKeyStoreKey, Label: "Resy API Key", Description: "Authorization header value (after 'ResyAPI api_key=')"},
			credentialerror.Field{Key: ResyAuthTokenStoreKey, Label: "Resy Auth Token", Description: "x-resy-auth-token header value"},
		)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("resy API rate limit exceeded — wait a moment and try again")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("resy API %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	return json.RawMessage(body), nil
}
