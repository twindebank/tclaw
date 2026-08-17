// Package garmin is a client for Garmin Connect's unofficial cloud API.
//
// It talks to connectapi.garmin.com with a DI OAuth bearer token. The web route space
// (connect.garmin.com/proxy/...) is deliberately not supported: it authenticates with a browser
// session cookie rather than a bearer token, so nothing here can reach it.
//
// The package is split so the transport concerns stay separate from the domain:
//
//	garmin           authentication, tokens, request plumbing, device listing
//	garmin/settings  the typed settings model, including activity data screens
package garmin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is Garmin's API host.
const DefaultBaseURL = "https://connectapi.garmin.com"

const defaultTimeout = 30 * time.Second

// Client issues authenticated requests against the Garmin Connect API. It is safe for concurrent
// use. It is stateful only because it owns an http.Client and a token source — everything built on
// top of it is stateless.
type Client struct {
	baseURL     string
	httpClient  *http.Client
	tokenSource TokenSource
}

// Options configures a Client. Only TokenSource is required.
type Options struct {
	// TokenSource supplies (and refreshes) the bearer token.
	TokenSource TokenSource

	// BaseURL overrides the API host. Empty means DefaultBaseURL.
	BaseURL string

	// HTTPClient overrides the transport. Empty means a client with a 30s timeout.
	HTTPClient *http.Client
}

// New returns a Client configured by opts.
func New(opts Options) (*Client, error) {
	if opts.TokenSource == nil {
		return nil, fmt.Errorf("garmin: TokenSource is required")
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		httpClient:  httpClient,
		tokenSource: opts.TokenSource,
	}, nil
}

// Request describes a single API call. Body, if non-nil, is JSON-encoded.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Body   any

	// Accept overrides the Accept header. This matters more than usual: several Garmin routes
	// return 406 for the wrong content type even though they exist and would otherwise serve —
	// the settings-change FIT file is only reachable with application/octet-stream.
	Accept string
}

// Do sends a request and returns the raw response body. Non-2xx responses become *APIError.
func (c *Client) Do(ctx context.Context, req Request) ([]byte, error) {
	var body io.Reader
	if req.Body != nil {
		encoded, err := json.Marshal(req.Body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	target := c.baseURL + "/" + strings.TrimLeft(req.Path, "/")
	if len(req.Query) > 0 {
		target += "?" + req.Query.Encode()
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, target, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	token, err := c.tokenSource.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("obtain token: %w", err)
	}
	accept := req.Accept
	if accept == "" {
		accept = "application/json"
	}
	httpReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	httpReq.Header.Set("Accept", accept)
	if req.Body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	applyClientHeaders(httpReq.Header)

	response, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer closeBody(response)

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &APIError{
			StatusCode: response.StatusCode,
			Method:     req.Method,
			Path:       req.Path,
			Body:       string(raw),
		}
	}
	return raw, nil
}

// DoJSON sends a request and decodes the response into out.
func (c *Client) DoJSON(ctx context.Context, req Request, out any) error {
	raw, err := c.Do(ctx, req)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s %s response: %w", req.Method, req.Path, err)
	}
	return nil
}

// applyClientHeaders sets the headers Garmin's API expects from the mobile client. Requests that
// omit them are served inconsistently, so every request — including token refresh — carries them.
func applyClientHeaders(header http.Header) {
	header.Set("User-Agent", "GCM-Android-5.23")
	header.Set("X-Garmin-Client-Platform", "Android")
	header.Set("X-App-Ver", "10861")
	header.Set("X-Lang", "en")
	header.Set("Accept-Language", "en-US,en;q=0.9")
}

func readAll(response *http.Response) (string, error) {
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
