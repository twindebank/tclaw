package discovery

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const protocolVersion = "2025-03-26"

// AuthMetadata holds the combined OAuth 2.1 metadata discovered from
// the MCP server's protected resource metadata and the authorization server's
// well-known configuration.
type AuthMetadata struct {
	// From protected resource metadata
	ResourceURL string

	// From auth server metadata
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string // may be empty if server doesn't support dynamic registration
	Issuer                string

	// ScopesSupported comes from the protected resource metadata, not the
	// auth server's — it is the resource that declares what a token needs to
	// carry to be accepted.
	ScopesSupported []string
}

// ClientRegistration holds the credentials returned by RFC 7591
// dynamic client registration.
type ClientRegistration struct {
	ClientID     string
	ClientSecret string // may be empty for public clients
}

// RemoteCredentials holds the tokens returned by an OAuth token exchange.
type RemoteCredentials struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // seconds
}

// DiscoverAuthOption customises the HTTP behaviour of the OAuth discovery
// calls — DiscoverAuth, RegisterClient and RedirectURIAccepted.
type DiscoverAuthOption func(*discoverAuthConfig)

type discoverAuthConfig struct {
	client *http.Client
}

// WithDiscoverAuthHTTPClient overrides the default SSRF-safe HTTP client used
// for every request DiscoverAuth makes (the initial probe, and the
// resource/auth-server metadata fetches) AND skips the SSRF URL validation on
// all of them — the caller is presumed to have pinned their own trust.
// Mirrors ListTools's WithHTTPClient; primarily for tests that want to talk
// to httptest.NewTLSServer instances on 127.0.0.1.
func WithDiscoverAuthHTTPClient(c *http.Client) DiscoverAuthOption {
	return func(cfg *discoverAuthConfig) { cfg.client = c }
}

// DiscoverAuth probes an MCP server URL to determine whether OAuth is required.
// It sends a minimal JSON-RPC initialize request; if the server returns 401 with
// a WWW-Authenticate header, it follows the OAuth 2.1 discovery chain:
//
//  1. Parse resource_metadata URL from WWW-Authenticate header
//  2. Fetch protected resource metadata → get authorization_servers[0]
//  3. Fetch auth server's .well-known/oauth-authorization-server → get endpoints
//
// Returns nil metadata (no error) if the server does not require auth (2xx
// response) to the initial probe.
//
// A non-nil error means discovery could not determine the auth requirement
// (network failure, an unexpected status code, or a malformed/incomplete
// metadata chain) — callers MUST NOT treat this the same as "no auth
// required". Some servers (e.g. those sitting behind a WAF or bot-protection
// layer) reject every request, authenticated or not, with a bare non-2xx
// status and no WWW-Authenticate header at all; silently registering those as
// auth-free produces a permanently broken, zero-tool registration.
func DiscoverAuth(ctx context.Context, mcpURL string, opts ...DiscoverAuthOption) (*AuthMetadata, error) {
	client, skipValidation := resolveClient(opts)
	if !skipValidation {
		// Validate the MCP URL before making any outbound requests.
		if err := validateExternalURL(mcpURL); err != nil {
			return nil, fmt.Errorf("unsafe MCP URL: %w", err)
		}
	}

	// Send a minimal MCP initialize request to provoke a 401 if auth is needed.
	probe := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params: mustMarshal(map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]string{"name": "tclaw", "version": "0.1.0"},
		}),
	}

	body, err := json.Marshal(probe)
	if err != nil {
		return nil, fmt.Errorf("marshal probe request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create probe request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("probe MCP server: %w", err)
	}
	defer resp.Body.Close()
	// Drain (capped) body so the connection can be reused.
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBodyBytes))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil, nil
	}

	if resp.StatusCode != http.StatusUnauthorized {
		return nil, fmt.Errorf("unexpected status from MCP server: %d", resp.StatusCode)
	}

	// Parse resource_metadata URL from WWW-Authenticate header.
	resourceMetaURL, err := parseResourceMetadataURL(resp.Header.Get("WWW-Authenticate"), mcpURL)
	if err != nil {
		return nil, fmt.Errorf("parse WWW-Authenticate: %w", err)
	}

	if !skipValidation {
		// Validate the resource metadata URL before fetching (SSRF protection —
		// this URL comes from the remote server's WWW-Authenticate header).
		if err := validateExternalURL(resourceMetaURL); err != nil {
			return nil, fmt.Errorf("unsafe resource metadata URL: %w", err)
		}
	}

	// Fetch protected resource metadata to find the authorization server.
	resourceMeta, err := fetchJSON[protectedResourceMeta](ctx, client, resourceMetaURL)
	if err != nil {
		return nil, fmt.Errorf("fetch protected resource metadata: %w", err)
	}
	if len(resourceMeta.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("protected resource metadata has no authorization_servers")
	}

	authServerURL := resourceMeta.AuthorizationServers[0]

	// Fetch the auth server's well-known configuration.
	wellKnownURL, err := buildWellKnownURL(authServerURL)
	if err != nil {
		return nil, fmt.Errorf("build auth server well-known URL: %w", err)
	}

	if !skipValidation {
		// Validate the well-known URL (SSRF protection — derived from
		// authorization_servers[0] in the resource metadata response).
		if err := validateExternalURL(wellKnownURL); err != nil {
			return nil, fmt.Errorf("unsafe auth server URL: %w", err)
		}
	}

	asMeta, err := fetchJSON[authServerMeta](ctx, client, wellKnownURL)
	if err != nil {
		return nil, fmt.Errorf("fetch auth server metadata: %w", err)
	}

	return &AuthMetadata{
		ResourceURL:           resourceMeta.Resource,
		AuthorizationEndpoint: asMeta.AuthorizationEndpoint,
		TokenEndpoint:         asMeta.TokenEndpoint,
		RegistrationEndpoint:  asMeta.RegistrationEndpoint,
		Issuer:                asMeta.Issuer,
		ScopesSupported:       resourceMeta.ScopesSupported,
	}, nil
}

// RegisterClientParams are the inputs to RFC 7591 dynamic client registration.
type RegisterClientParams struct {
	Meta        *AuthMetadata
	RedirectURI string

	// ClientName is sent as the RFC 7591 `client_name`. Some authorization
	// servers reject a registration that omits it — Strava's MCP issuer
	// answers `invalid_client_metadata` unless a recognised name is present,
	// because its "dynamic" registration is really an allowlist that maps a
	// known client onto one pre-provisioned app. Empty omits the field.
	ClientName string
}

// RegisterClient performs RFC 7591 dynamic client registration against the
// auth server's registration endpoint.
func RegisterClient(ctx context.Context, params RegisterClientParams, opts ...DiscoverAuthOption) (*ClientRegistration, error) {
	if params.Meta.RegistrationEndpoint == "" {
		return nil, fmt.Errorf("auth server does not support dynamic client registration")
	}

	client, skipValidation := resolveClient(opts)
	if !skipValidation {
		if err := validateExternalURL(params.Meta.RegistrationEndpoint); err != nil {
			return nil, fmt.Errorf("unsafe registration endpoint: %w", err)
		}
	}

	regReq := clientRegistrationRequest{
		RedirectURIs:            []string{params.RedirectURI},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "client_secret_basic",
		ClientName:              params.ClientName,
	}

	body, err := json.Marshal(regReq)
	if err != nil {
		return nil, fmt.Errorf("marshal registration request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, params.Meta.RegistrationEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Some issuers content-negotiate their registration endpoint and answer
	// with an HTML error page unless JSON is explicitly requested.
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("register client: %w", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, maxResponseBodyBytes)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(limited)
		return nil, fmt.Errorf("registration failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var regResp clientRegistrationResponse
	if err := json.NewDecoder(limited).Decode(&regResp); err != nil {
		return nil, fmt.Errorf("decode registration response: %w", err)
	}

	return &ClientRegistration{
		ClientID:     regResp.ClientID,
		ClientSecret: regResp.ClientSecret,
	}, nil
}

// AuthURLParams are the inputs to BuildAuthURLWithPKCE.
type AuthURLParams struct {
	Meta        *AuthMetadata
	Reg         *ClientRegistration
	State       string
	RedirectURI string
	MCPURL      string

	// CodeVerifier reuses an existing PKCE verifier instead of generating a
	// fresh one. Set it when rebuilding the same authorization URL (e.g. to
	// swap in the real state token) so the challenge still matches the
	// verifier the token exchange will present. Empty generates a new one.
	CodeVerifier string
}

// BuildAuthURLWithPKCE constructs the OAuth authorization URL with PKCE (S256)
// and the resource parameter (RFC 8707). Returns the full URL and the PKCE
// code verifier (needed later for the token exchange).
//
// Scopes come from the protected resource metadata: an authorization server
// that publishes scopes_supported generally grants only what was asked for, so
// omitting them can yield a token that the resource then refuses.
func BuildAuthURLWithPKCE(p AuthURLParams) (authURL string, codeVerifier string) {
	codeVerifier = p.CodeVerifier
	if codeVerifier == "" {
		codeVerifier = generateCodeVerifier()
	}
	codeChallenge := computeCodeChallenge(codeVerifier)

	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {p.Reg.ClientID},
		"redirect_uri":          {p.RedirectURI},
		"state":                 {p.State},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"resource":              {p.MCPURL},
	}
	if len(p.Meta.ScopesSupported) > 0 {
		params.Set("scope", strings.Join(p.Meta.ScopesSupported, " "))
	}

	authURL = p.Meta.AuthorizationEndpoint + "?" + params.Encode()
	return authURL, codeVerifier
}

// RedirectURIAccepted reports whether the authorization server will accept
// authURL's redirect_uri, by issuing the authorization request without
// following redirects and reading the status.
//
// This exists because an authorization server may accept any redirect_uri at
// registration time and only validate it at the authorization endpoint —
// Strava echoes back whatever redirect_uris a registration sends, then rejects
// everything but a loopback address when the user actually arrives. Without
// this probe the mismatch surfaces as a dead end in the user's browser, long
// after the tool call reported success.
//
// A 2xx or 3xx means the request was accepted (the redirect is typically to a
// login or consent page). A 4xx means it was rejected. Any other status, or a
// transport failure, returns an error — the caller must not read that as
// rejection, since the redirect URI may well be fine.
func RedirectURIAccepted(ctx context.Context, authURL string, opts ...DiscoverAuthOption) (bool, error) {
	client, skipValidation := resolveClient(opts)
	if !skipValidation {
		if err := validateExternalURL(authURL); err != nil {
			return false, fmt.Errorf("unsafe authorization URL: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	if err != nil {
		return false, fmt.Errorf("create authorize probe request: %w", err)
	}

	// Never follow the redirect: it leads to a login page on an unrelated
	// host and tells us nothing beyond the status we already have.
	probeClient := *client
	probeClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := probeClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("probe authorize endpoint: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBodyBytes))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 400:
		return true, nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return false, nil
	default:
		return false, fmt.Errorf("unexpected status %d from authorize endpoint", resp.StatusCode)
	}
}

// ExchangeCodeWithPKCE exchanges an authorization code for tokens, including
// the PKCE code_verifier and resource parameter (RFC 8707).
func ExchangeCodeWithPKCE(ctx context.Context, meta *AuthMetadata, reg *ClientRegistration, code, codeVerifier, redirectURI, mcpURL string, opts ...DiscoverAuthOption) (*RemoteCredentials, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
		"client_id":     {reg.ClientID},
		"resource":      {mcpURL},
	}

	return doTokenRequest(ctx, meta.TokenEndpoint, reg, form, opts...)
}

// RefreshRemoteToken uses a refresh token to obtain a new access token.
func RefreshRemoteToken(ctx context.Context, meta *AuthMetadata, reg *ClientRegistration, refreshToken, mcpURL string, opts ...DiscoverAuthOption) (*RemoteCredentials, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {reg.ClientID},
		"resource":      {mcpURL},
	}

	return doTokenRequest(ctx, meta.TokenEndpoint, reg, form, opts...)
}

// --- Internal helpers ---

// resolveClient applies the options and returns the HTTP client to use, plus
// whether SSRF URL validation should be skipped. An injected client means the
// caller has pinned their own trust (tests against a loopback TLS server), so
// the HTTPS-only/public-address rules would only get in the way.
func resolveClient(opts []DiscoverAuthOption) (client *http.Client, skipValidation bool) {
	cfg := discoverAuthConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.client != nil {
		return cfg.client, true
	}
	return safeClient, false
}

// doTokenRequest sends a form-encoded POST to the token endpoint and decodes
// the response into RemoteCredentials. Adds client_secret via Basic auth if present.
func doTokenRequest(ctx context.Context, tokenEndpoint string, reg *ClientRegistration, form url.Values, opts ...DiscoverAuthOption) (*RemoteCredentials, error) {
	client, skipValidation := resolveClient(opts)
	if !skipValidation {
		if err := validateExternalURL(tokenEndpoint); err != nil {
			return nil, fmt.Errorf("unsafe token endpoint: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if reg.ClientSecret != "" {
		req.SetBasicAuth(reg.ClientID, reg.ClientSecret)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, maxResponseBodyBytes)

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(limited)
		return nil, fmt.Errorf("token request failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tokenResp tokenResponse
	if err := json.NewDecoder(limited).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}

	return &RemoteCredentials{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresIn:    tokenResp.ExpiresIn,
	}, nil
}

// parseResourceMetadataURL extracts the resource_metadata URL from a
// WWW-Authenticate header. Falls back to constructing
// <origin>/.well-known/oauth-protected-resource if the header doesn't
// contain a resource_metadata parameter.
func parseResourceMetadataURL(header string, mcpURL string) (string, error) {
	if header != "" {
		// Look for resource_metadata="<url>" in the header value.
		const key = `resource_metadata="`
		if idx := strings.Index(header, key); idx >= 0 {
			rest := header[idx+len(key):]
			if end := strings.Index(rest, `"`); end >= 0 {
				return rest[:end], nil
			}
		}
	}

	// Fallback: construct from the MCP server's origin.
	parsed, err := url.Parse(mcpURL)
	if err != nil {
		return "", fmt.Errorf("parse MCP URL: %w", err)
	}

	fallback := fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource", parsed.Scheme, parsed.Host)
	return fallback, nil
}

// buildWellKnownURL constructs the .well-known/oauth-authorization-server URL
// from an authorization server URL.
func buildWellKnownURL(authServerURL string) (string, error) {
	parsed, err := url.Parse(authServerURL)
	if err != nil {
		return "", fmt.Errorf("parse auth server URL: %w", err)
	}

	// RFC 8414 §3.1: the well-known segment is INSERTED between host and the
	// issuer's path component, not appended to the origin.
	path := strings.TrimSuffix(parsed.Path, "/")
	wellKnown := fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server%s", parsed.Scheme, parsed.Host, path)
	return wellKnown, nil
}

// fetchJSON GETs a URL and decodes the JSON response into T.
// Response bodies are capped at maxResponseBodyBytes to prevent memory exhaustion.
func fetchJSON[T any](ctx context.Context, client *http.Client, rawURL string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", rawURL, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	// Cap response body to prevent memory exhaustion from oversized payloads.
	limited := io.LimitReader(resp.Body, maxResponseBodyBytes)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(limited)
		return nil, fmt.Errorf("fetch %s returned status %d: %s", rawURL, resp.StatusCode, string(body))
	}

	var result T
	if err := json.NewDecoder(limited).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response from %s: %w", rawURL, err)
	}

	return &result, nil
}

// generateCodeVerifier creates a random 32-byte PKCE code verifier,
// base64url-encoded without padding (per RFC 7636).
func generateCodeVerifier() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read only fails on catastrophic system errors.
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// computeCodeChallenge returns base64url(sha256(verifier)) for PKCE S256.
func computeCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mustMarshal: %v", err))
	}
	return data
}

// --- Wire types for JSON decoding ---

// jsonRPCRequest is a minimal JSON-RPC request for probing MCP servers.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type protectedResourceMeta struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

type authServerMeta struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
}

type clientRegistrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ClientName              string   `json:"client_name,omitempty"`
}

type clientRegistrationResponse struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}
