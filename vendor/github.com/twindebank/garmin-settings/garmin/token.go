package garmin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// diTokenURL is Garmin's OAuth token endpoint. It lives on a different host to the API.
const diTokenURL = "https://diauth.garmin.com/di-oauth2-service/oauth/token"

// refreshLeeway renews the access token this far before it actually expires, so a long-running call
// cannot start with a valid token and finish with an expired one.
const refreshLeeway = 15 * time.Minute

// Token is a Garmin OAuth credential set. The access token is short-lived (~26h); the refresh token
// is what actually keeps a headless process working, and Garmin may rotate it on each refresh.
type Token struct {
	AccessToken  string `json:"di_token"`
	RefreshToken string `json:"di_refresh_token"`
	ClientID     string `json:"di_client_id"`
}

// Valid reports whether the token can still be used without refreshing.
func (t Token) Valid() bool {
	expiry, err := t.Expiry()
	if err != nil {
		return false
	}
	return time.Now().Add(refreshLeeway).Before(expiry)
}

// Expiry reads the `exp` claim out of the access token. The JWT signature is not verified — only
// Garmin can do that, and the claim is used solely to decide when to refresh.
func (t Token) Expiry() (time.Time, error) {
	parts := strings.Split(t.AccessToken, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("malformed access token: want 3 JWT parts, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("decode token payload: %w", err)
	}
	var claims struct {
		Expiry int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("parse token claims: %w", err)
	}
	if claims.Expiry == 0 {
		return time.Time{}, fmt.Errorf("access token has no exp claim")
	}
	return time.Unix(claims.Expiry, 0), nil
}

// TokenStore persists a token between processes. Implementations must be safe for concurrent use.
type TokenStore interface {
	Load(ctx context.Context) (Token, error)
	Save(ctx context.Context, token Token) error
}

// TokenSource yields a currently-valid access token, refreshing as needed.
type TokenSource interface {
	Token(ctx context.Context) (Token, error)
}

// refreshingSource is the normal way to authenticate: it reads a token from a store, refreshes it
// when it is close to expiry, and writes the rotated token back. Because Garmin may issue a new
// refresh token on every refresh, failing to persist the result would eventually strand the caller
// needing a fresh MFA login — so a save failure is surfaced, not swallowed.
type refreshingSource struct {
	store  TokenStore
	client *http.Client

	mu     sync.Mutex
	cached Token
}

// NewRefreshingTokenSource returns a TokenSource backed by store.
func NewRefreshingTokenSource(store TokenStore, client *http.Client) TokenSource {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &refreshingSource{store: store, client: client}
}

func (s *refreshingSource) Token(ctx context.Context) (Token, error) {
	// Serialised so concurrent API calls cannot each trigger their own refresh and race to persist
	// different rotated refresh tokens.
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cached.Valid() {
		return s.cached, nil
	}

	token := s.cached
	if token.RefreshToken == "" {
		loaded, err := s.store.Load(ctx)
		if err != nil {
			return Token{}, fmt.Errorf("load token: %w", err)
		}
		token = loaded
	}
	if token.Valid() {
		s.cached = token
		return token, nil
	}
	if token.RefreshToken == "" || token.ClientID == "" {
		return Token{}, ErrNoToken
	}

	refreshed, err := refresh(ctx, s.client, token)
	if err != nil {
		return Token{}, fmt.Errorf("refresh token: %w", err)
	}
	if err := s.store.Save(ctx, refreshed); err != nil {
		return Token{}, fmt.Errorf("persist refreshed token: %w", err)
	}

	s.cached = refreshed
	return refreshed, nil
}

// StaticTokenSource returns a TokenSource that always yields token. Useful for tests and for
// callers that manage refresh themselves.
func StaticTokenSource(token Token) TokenSource { return staticSource{token: token} }

type staticSource struct{ token Token }

func (s staticSource) Token(context.Context) (Token, error) { return s.token, nil }

// refresh exchanges a refresh token for a new access token. Garmin authenticates the client with
// HTTP Basic where the client id is the username and the password is empty.
func refresh(ctx context.Context, client *http.Client, token Token) (Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {token.ClientID},
		"refresh_token": {token.RefreshToken},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, diTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("build refresh request: %w", err)
	}
	basic := base64.StdEncoding.EncodeToString([]byte(token.ClientID + ":"))
	request.Header.Set("Authorization", "Basic "+basic)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	applyClientHeaders(request.Header)

	response, err := client.Do(request)
	if err != nil {
		return Token{}, fmt.Errorf("send refresh request: %w", err)
	}
	defer closeBody(response)

	body, err := readAll(response)
	if err != nil {
		return Token{}, fmt.Errorf("read refresh response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return Token{}, &APIError{StatusCode: response.StatusCode, Method: http.MethodPost, Path: diTokenURL, Body: body}
	}

	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return Token{}, fmt.Errorf("parse refresh response: %w", err)
	}
	if payload.AccessToken == "" {
		return Token{}, fmt.Errorf("refresh response contained no access token")
	}

	refreshed := Token{AccessToken: payload.AccessToken, RefreshToken: token.RefreshToken, ClientID: token.ClientID}
	// Garmin rotates the refresh token on some refreshes and omits it on others; keep the old one
	// when it is absent rather than blanking a still-valid credential.
	if payload.RefreshToken != "" {
		refreshed.RefreshToken = payload.RefreshToken
	}
	return refreshed, nil
}
