package remotemcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"tclaw/internal/mcp"
	"tclaw/internal/mcp/discovery"
)

const ToolRemoteMCPAuthComplete = "remote_mcp_auth_complete"

// maxCallbackURLLength bounds the pasted value so a malformed paste cannot
// push an unbounded string through URL parsing.
const maxCallbackURLLength = 8192

func remoteMCPAuthCompleteDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolRemoteMCPAuthComplete,
		Description: "Complete a manual (loopback) remote MCP OAuth authorization using the callback " +
			"URL the user pasted back. Only for servers registered with status 'pending_manual_auth', " +
			"where the authorization server redirects to a loopback address that cannot reach tclaw. " +
			"The user's browser will show a connection error on that page — the URL in the address bar " +
			"is still the thing needed here. Pass it verbatim; it carries the one-time code and the " +
			"state token. For servers using the normal hosted callback, use remote_mcp_auth_wait instead.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "The remote MCP name whose authorization is pending."
				},
				"callback_url": {
					"type": "string",
					"description": "The full URL from the user's address bar after they approved access, e.g. 'http://localhost:47821/callback?code=...&state=...'. Paste it exactly as given, including the query string."
				}
			},
			"required": ["name", "callback_url"]
		}`),
	}
}

type remoteMCPAuthCompleteArgs struct {
	Name        string `json:"name"`
	CallbackURL string `json:"callback_url"`
}

func remoteMCPAuthCompleteHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a remoteMCPAuthCompleteArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Name == "" {
			return nil, fmt.Errorf("name is required")
		}
		if len(a.CallbackURL) > maxCallbackURLLength {
			return nil, fmt.Errorf("callback_url exceeds %d characters", maxCallbackURLLength)
		}

		entry, err := deps.Manager.GetRemoteMCP(ctx, a.Name)
		if err != nil {
			return nil, fmt.Errorf("look up remote mcp %q: %w", a.Name, err)
		}
		if entry == nil {
			return nil, fmt.Errorf("no remote MCP named %q — run remote_mcp_add first", a.Name)
		}

		auth, err := deps.Manager.GetRemoteMCPAuth(ctx, a.Name)
		if err != nil {
			return nil, fmt.Errorf("load auth for %q: %w", a.Name, err)
		}
		if auth == nil {
			return nil, fmt.Errorf("no stored auth metadata for %q — run remote_mcp_add first", a.Name)
		}
		if auth.AccessToken != "" && auth.PendingExchange == nil {
			// Already authorized — finish the registration rather than
			// failing, so a duplicate paste is harmless.
			return finalizeAuthorized(ctx, deps, a.Name, auth)
		}
		if auth.PendingExchange == nil {
			return nil, fmt.Errorf("no manual authorization is pending for %q — run remote_mcp_add again to start one", a.Name)
		}
		if auth.PendingExchange.Expired() {
			return nil, fmt.Errorf("the authorization for %q expired before the code was pasted back — run remote_mcp_add again to start a fresh one", a.Name)
		}

		code, err := parseCallbackCode(a.CallbackURL, auth.PendingExchange.State)
		if err != nil {
			return nil, err
		}

		authMeta := &discovery.AuthMetadata{
			ResourceURL:           entry.URL,
			AuthorizationEndpoint: auth.AuthorizationEndpoint,
			TokenEndpoint:         auth.TokenEndpoint,
			RegistrationEndpoint:  auth.RegistrationEndpoint,
			Issuer:                auth.AuthServerIssuer,
		}
		reg := &discovery.ClientRegistration{ClientID: auth.ClientID, ClientSecret: auth.ClientSecret}

		creds, err := discovery.ExchangeCodeWithPKCE(ctx, authMeta, reg, code,
			auth.PendingExchange.CodeVerifier, auth.PendingExchange.RedirectURI, entry.URL,
			discoverAuthOpts(deps)...)
		if err != nil {
			return nil, fmt.Errorf("exchange authorization code for %q: %w", a.Name, err)
		}
		if creds.AccessToken == "" {
			return nil, fmt.Errorf("authorization server returned no access token for %q", a.Name)
		}

		auth.AccessToken = creds.AccessToken
		auth.RefreshToken = creds.RefreshToken
		if creds.ExpiresIn > 0 {
			auth.TokenExpiry = time.Now().Add(time.Duration(creds.ExpiresIn) * time.Second)
		}
		// The code is spent; clearing this makes a replayed paste fall through
		// to the already-authorized path instead of a second exchange.
		auth.PendingExchange = nil

		if err := deps.Manager.SetRemoteMCPAuth(ctx, a.Name, auth); err != nil {
			return nil, fmt.Errorf("store tokens for %q: %w", a.Name, err)
		}

		slog.Info("completed manual OAuth for remote mcp", "name", a.Name, "has_refresh", creds.RefreshToken != "")

		return finalizeAuthorized(ctx, deps, a.Name, auth)
	}
}

// parseCallbackCode extracts the authorization code from a pasted callback
// URL, verifying the state token matches the one this flow issued.
//
// The paste arrives via chat, so it may be a bare query string, carry
// surrounding whitespace, or be an error redirect rather than a success one —
// each of those gets a message the agent can act on rather than a parse
// failure.
func parseCallbackCode(pasted, expectedState string) (string, error) {
	pasted = strings.TrimSpace(pasted)
	if pasted == "" {
		return "", fmt.Errorf("callback_url is empty — ask the user for the full URL from their browser's address bar")
	}

	// Accept a bare query string ("?code=...&state=..." or "code=...") as
	// well as a full URL, since users copy either.
	toParse := pasted
	if !strings.Contains(toParse, "://") {
		toParse = "http://localhost/?" + strings.TrimPrefix(toParse, "?")
	}

	parsed, err := url.Parse(toParse)
	if err != nil {
		return "", fmt.Errorf("could not parse callback_url — ask the user to paste the full URL from the address bar: %w", err)
	}
	query := parsed.Query()

	if oauthErr := query.Get("error"); oauthErr != "" {
		desc := query.Get("error_description")
		if desc == "" {
			desc = "no description given"
		}
		return "", fmt.Errorf("the authorization was refused (%s: %s) — the user may have declined, or the request expired", oauthErr, desc)
	}

	code := query.Get("code")
	if code == "" {
		return "", fmt.Errorf("callback_url has no 'code' parameter — the user may have copied the URL before approving, or copied the wrong page")
	}

	gotState := query.Get("state")
	if gotState == "" {
		return "", fmt.Errorf("callback_url has no 'state' parameter — it does not look like the callback from this authorization")
	}
	// Constant-time compare: state is the CSRF defence for this flow.
	if subtle.ConstantTimeCompare([]byte(gotState), []byte(expectedState)) != 1 {
		return "", fmt.Errorf("callback state does not match the pending authorization — this URL belongs to a different or older authorization attempt")
	}

	return code, nil
}
