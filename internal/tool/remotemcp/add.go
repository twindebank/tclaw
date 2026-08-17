package remotemcp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"time"

	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/mcp/discovery"
	"tclaw/internal/remotemcpstore"
)

const (
	maxMCPNameLength     = 64
	maxMCPURLLength      = 2048
	maxHeaderNameLength  = 128
	maxHeaderValueLength = 4096
	maxHeaders           = 16
)

var (
	mcpNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

	// headerNamePattern matches RFC 7230 token chars (header field names).
	headerNamePattern = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)
)

const ToolRemoteMCPAdd = "remote_mcp_add"

// oauthClientName is sent as `client_name` on RFC 7591 dynamic registration.
// Some authorization servers treat registration as an allowlist rather than
// true dynamic registration and reject any name they do not recognise —
// Strava's MCP issuer answers `invalid_client_metadata` for an unknown one and
// maps every accepted registration onto a single pre-provisioned app. tclaw
// runs the Claude Code CLI, so this names the client the user is actually
// authorizing.
const oauthClientName = "Claude Code"

// manualRedirectURI is the loopback callback offered to authorization servers
// that refuse tclaw's hosted HTTPS callback. Nothing listens on it: the point
// is only that the browser lands on a URL carrying the code, which the user
// copies back. The port is arbitrary and deliberately high to avoid colliding
// with anything the user might actually be running.
const manualRedirectURI = "http://localhost:47821/callback"

func remoteMCPAddDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolRemoteMCPAdd,
		Description: "Connect a remote MCP server. By default, discovers OAuth requirements " +
			"automatically and returns an authorization URL if needed. For servers that use a " +
			"non-OAuth auth scheme (static tokens, custom auth headers, unguessable URL path, etc.), " +
			"pass skip_auth_discovery=true and attach credentials via 'headers' (inline) or " +
			"'header_secret_keys' (resolved from the secret store — recommended for any sensitive " +
			"value, since it avoids sending the value through chat). If the URL itself contains a " +
			"secret, pass 'url_secret_key' instead of 'url'. All stored credentials and URLs are " +
			"encrypted at rest.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"url": {
					"type": "string",
					"description": "The MCP server URL (e.g. 'https://mcp.example.com/sse'). Use this when the URL is not sensitive. For URLs containing a secret (e.g. an unguessable path segment), use url_secret_key instead. Exactly one of url or url_secret_key must be provided."
				},
				"url_secret_key": {
					"type": "string",
					"description": "Secret store key whose value is the MCP server URL. Use this when the URL itself is sensitive so it never passes through chat. The key must already be set via a prior secret_form_request. Exactly one of url or url_secret_key must be provided."
				},
				"name": {
					"type": "string",
					"description": "A short label for this server (e.g. 'linear', 'notion'). Used as the MCP server name in tool prefixes."
				},
				"channel": {
					"type": "string",
					"description": "Channel name to scope this remote MCP to. Its tools will only be available on this channel."
				},
				"skip_auth_discovery": {
					"type": "boolean",
					"description": "If true, skip OAuth discovery entirely. Use when the server uses a non-OAuth auth scheme — combine with 'headers' or 'header_secret_keys' to attach the credentials."
				},
				"headers": {
					"type": "object",
					"description": "Static headers to send on every request (e.g. {\"X-Tenant\": \"acme\"}). Values are inline — do NOT use this for secrets that arrived over chat. For secrets, collect them via secret_form_request and pass header_secret_keys instead. Requires skip_auth_discovery=true.",
					"additionalProperties": {"type": "string"}
				},
				"header_secret_keys": {
					"type": "object",
					"description": "Headers whose values are resolved from the secret store at registration time. Map of HTTP header name → secret store key. The referenced keys must already be set via a prior secret_form_request. Requires skip_auth_discovery=true.",
					"additionalProperties": {"type": "string"}
				},
				"manual_auth": {
					"type": "boolean",
					"description": "Force the manual (loopback) OAuth flow, where the user pastes the callback URL back instead of tclaw receiving it. Normally unnecessary — tclaw detects a server that refuses its hosted callback and switches automatically. Set it only if a server accepts the hosted callback at the authorization endpoint but still fails to deliver the callback."
				},
				"tls_pin_sha256": {
					"type": "string",
					"description": "Pin the server's TLS certificate by its SHA-256 fingerprint (hex, e.g. from 'openssl x509 -fingerprint -sha256'). Use for a self-signed https server on a Fly private host (*.flycast/*.internal) where no public CA applies — it authenticates the server by exact cert, not the system trust store. Non-secret. Requires an https URL."
				}
			},
			"required": ["name", "channel"]
		}`),
	}
}

type remoteMCPAddArgs struct {
	URL               string            `json:"url,omitempty"`
	URLSecretKey      string            `json:"url_secret_key,omitempty"`
	Name              string            `json:"name"`
	Channel           string            `json:"channel"`
	SkipAuthDiscovery bool              `json:"skip_auth_discovery,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	HeaderSecretKeys  map[string]string `json:"header_secret_keys,omitempty"`
	TLSPinSHA256      string            `json:"tls_pin_sha256,omitempty"`
	ManualAuth        bool              `json:"manual_auth,omitempty"`
}

func remoteMCPAddHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a remoteMCPAddArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Name == "" || len(a.Name) > maxMCPNameLength || !mcpNamePattern.MatchString(a.Name) {
			return nil, fmt.Errorf("name must be 1-%d characters, alphanumeric with hyphens/underscores", maxMCPNameLength)
		}
		if a.Channel == "" {
			return nil, fmt.Errorf("channel is required — specify which channel this remote MCP's tools should be available on")
		}

		// Resolve the URL — exactly one of url or url_secret_key must be provided.
		// url_secret_key keeps the URL out of chat history when it contains a
		// secret path segment (e.g. ha-mcp's /private_<random>).
		resolvedURL, err := resolveURL(ctx, deps.SecretStore, a.URL, a.URLSecretKey)
		if err != nil {
			return nil, err
		}
		parsed, err := url.Parse(resolvedURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("url must be a valid absolute URL (e.g. https://mcp.example.com/sse)")
		}
		// Public hosts must use HTTPS. http is allowed only for Fly private hosts
		// (*.flycast/*.internal), which are reachable only over the encrypted 6PN.
		if parsed.Scheme != "https" && !(parsed.Scheme == "http" && discovery.IsFlyPrivateHost(parsed.Hostname())) {
			return nil, fmt.Errorf("only HTTPS MCP server URLs are allowed (http permitted only for Fly private hosts: *.flycast, *.internal)")
		}

		// A cert pin only applies to a TLS connection, and must be a valid
		// SHA-256 fingerprint. Reject early so a typo can't silently fall back
		// to unpinned behaviour.
		if a.TLSPinSHA256 != "" {
			if parsed.Scheme != "https" {
				return nil, fmt.Errorf("tls_pin_sha256 requires an https URL")
			}
			if _, err := discovery.ParsePin(a.TLSPinSHA256); err != nil {
				return nil, err
			}
		}

		hasAnyHeaders := len(a.Headers) > 0 || len(a.HeaderSecretKeys) > 0
		if hasAnyHeaders && !a.SkipAuthDiscovery {
			return nil, fmt.Errorf("headers require skip_auth_discovery=true — combining OAuth with static headers is not currently supported")
		}

		// Resolve header_secret_keys from the secret store and merge with inline
		// headers. Rejecting duplicates keeps intent explicit: if a header name
		// appears in both maps the caller is confused about where the value comes from.
		resolvedHeaders, err := resolveHeaderSecretKeys(ctx, deps.SecretStore, a.HeaderSecretKeys)
		if err != nil {
			return nil, err
		}
		mergedHeaders, err := mergeHeaderMaps(a.Headers, resolvedHeaders)
		if err != nil {
			return nil, err
		}
		if err := validateHeaders(mergedHeaders); err != nil {
			return nil, err
		}

		// For non-OAuth registrations (skip_auth_discovery), fetch the tool
		// list up front. We need it for glob expansion at agent-start time —
		// the Claude CLI's --allowedTools flag does not honour wildcards for
		// MCP tools, so without an explicit list it refuses every tool call.
		// Failing the add on an unreachable server is correct: it keeps the
		// store clean and surfaces the real error to the user now, not later.
		if a.SkipAuthDiscovery {
			listOpts := listToolsOpts(deps)
			// When a pin is set and no client was injected (i.e. real use, not a
			// test), discover over a pinned-TLS client so the self-signed cert is
			// authenticated exactly as it will be at runtime.
			if a.TLSPinSHA256 != "" && deps.HTTPClient == nil {
				pinnedClient, perr := discovery.NewPinnedSafeClient(a.TLSPinSHA256)
				if perr != nil {
					return nil, perr
				}
				listOpts = append(listOpts, discovery.WithHTTPClient(pinnedClient))
			}
			discovered, listErr := discovery.ListTools(ctx, resolvedURL, mergedHeaders, listOpts...)
			if listErr != nil {
				return nil, fmt.Errorf("failed to list tools from remote MCP %q: %w", a.Name, listErr)
			}
			if len(discovered.ToolNames) == 0 {
				return nil, fmt.Errorf("remote MCP %q exposed no tools", a.Name)
			}

			entry, err := deps.Manager.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{
				Name:         a.Name,
				URL:          resolvedURL,
				Channel:      a.Channel,
				URLSensitive: a.URLSecretKey != "",
				ToolNames:    discovered.ToolNames,
				TLSPinSHA256: a.TLSPinSHA256,
				Instructions: discovered.Instructions,
			})
			if err != nil {
				return nil, fmt.Errorf("add remote mcp: %w", err)
			}

			if len(mergedHeaders) > 0 {
				authData := &remotemcpstore.RemoteMCPAuth{StaticHeaders: mergedHeaders}
				if err := deps.Manager.SetRemoteMCPAuth(ctx, a.Name, authData); err != nil {
					return nil, fmt.Errorf("store static headers: %w", err)
				}
			}
			if updateErr := deps.ConfigUpdater(ctx); updateErr != nil {
				return nil, fmt.Errorf("remote MCP %q added but config update failed — tools won't be available until next restart: %w", a.Name, updateErr)
			}
			// Restart the agent so the CLI picks up the expanded tool
			// allowlist. Without this the new tools stay invisible until
			// idle timeout fires naturally.
			if deps.OnChannelChange != nil {
				deps.OnChannelChange()
			}
			result := buildAddResponse(entry, "ready",
				fmt.Sprintf("Remote MCP %q added with %d tool(s) and %d static header(s) attached. Its tools will be available on the next message.", a.Name, len(discovered.ToolNames), len(mergedHeaders)))
			return json.Marshal(result)
		}

		slog.Info("discovering auth for remote MCP", "name", a.Name, "host", parsed.Host)

		// Discover whether OAuth is required. Run this BEFORE touching the
		// store: a discovery failure must not silently register the server as
		// "no auth needed" with zero tools and no way to authorize it later —
		// that leaves the user permanently stuck (e.g. a server whose probe
		// endpoint is blocked by a WAF/bot-protection layer and returns a
		// bare, non-compliant status with no WWW-Authenticate header at all,
		// such as Strava's hosted MCP server). Fail loudly instead so the
		// agent can retry with skip_auth_discovery=true if it knows the real
		// auth scheme, or surface the failure to the user.
		authMeta, err := discovery.DiscoverAuth(ctx, resolvedURL, discoverAuthOpts(deps)...)
		if err != nil {
			return nil, fmt.Errorf("could not determine whether remote MCP %q requires authentication — auth discovery probe failed: %w. Nothing was registered. If you know this server needs no auth, or uses a non-OAuth scheme (static token, custom header, etc.), retry with skip_auth_discovery=true and attach credentials via 'headers'/'header_secret_keys' if needed", a.Name, err)
		}

		// No auth needed — fetch the tool list and register.
		if authMeta == nil {
			discovered, listErr := discovery.ListTools(ctx, resolvedURL, nil, listToolsOpts(deps)...)
			if listErr != nil {
				return nil, fmt.Errorf("failed to list tools from remote MCP %q: %w", a.Name, listErr)
			}
			if len(discovered.ToolNames) == 0 {
				return nil, fmt.Errorf("remote MCP %q exposed no tools", a.Name)
			}
			entry, err := deps.Manager.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{
				Name:         a.Name,
				URL:          resolvedURL,
				Channel:      a.Channel,
				URLSensitive: a.URLSecretKey != "",
				ToolNames:    discovered.ToolNames,
				TLSPinSHA256: a.TLSPinSHA256,
				Instructions: discovered.Instructions,
			})
			if err != nil {
				return nil, fmt.Errorf("add remote mcp: %w", err)
			}
			if updateErr := deps.ConfigUpdater(ctx); updateErr != nil {
				return nil, fmt.Errorf("remote MCP %q added but config update failed — tools won't be available until next restart: %w", a.Name, updateErr)
			}
			if deps.OnChannelChange != nil {
				deps.OnChannelChange()
			}
			result := buildAddResponse(entry, "ready",
				fmt.Sprintf("Remote MCP %q added with %d tool(s). Its tools will be available on the next message.", a.Name, len(discovered.ToolNames)))
			return json.Marshal(result)
		}

		// OAuth required — start the flow.
		if deps.Callback == nil {
			return nil, fmt.Errorf("OAuth is required but no callback server is configured")
		}

		slog.Info("remote MCP requires OAuth", "name", a.Name, "issuer", authMeta.Issuer)

		callbackURL := deps.Callback.CallbackURL()

		// Dynamic client registration if supported.
		var reg *discovery.ClientRegistration
		if authMeta.RegistrationEndpoint != "" {
			reg, err = discovery.RegisterClient(ctx, discovery.RegisterClientParams{
				Meta:        authMeta,
				RedirectURI: callbackURL,
				ClientName:  oauthClientName,
			}, discoverAuthOpts(deps)...)
			if err != nil {
				return nil, fmt.Errorf("dynamic client registration: %w", err)
			}
			slog.Info("registered OAuth client", "name", a.Name, "client_id", reg.ClientID)
		} else {
			return nil, fmt.Errorf("remote MCP %q requires OAuth but does not support dynamic client registration — manual client_id configuration not yet supported", a.Name)
		}

		// Some authorization servers accept any redirect_uri at registration
		// but only permit a loopback address at the authorization endpoint.
		// Detect that here rather than letting the user discover it as a dead
		// end in their browser.
		redirectURI, manual, err := chooseRedirectURI(ctx, chooseRedirectParams{
			authMeta:    authMeta,
			reg:         reg,
			mcpURL:      resolvedURL,
			hostedURL:   callbackURL,
			forceManual: a.ManualAuth,
			opts:        discoverAuthOpts(deps),
		})
		if err != nil {
			return nil, fmt.Errorf("determine redirect uri for %q: %w", a.Name, err)
		}

		// Store the entry now that auth is confirmed required — it must
		// exist before the OAuth callback arrives (a separate, later
		// request) so remote_mcp_auth_wait can find it and attach the tool
		// list once the token exchange completes. Tool names are unknown
		// until after authorization, so they're left empty here.
		entry, err := deps.Manager.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{
			Name:         a.Name,
			URL:          resolvedURL,
			Channel:      a.Channel,
			URLSensitive: a.URLSecretKey != "",
			TLSPinSHA256: a.TLSPinSHA256,
		})
		if err != nil {
			return nil, fmt.Errorf("add remote mcp: %w", err)
		}

		// Store the auth metadata and registration before starting the flow,
		// so the callback handler can find it.
		authData := &remotemcpstore.RemoteMCPAuth{
			AuthServerIssuer:      authMeta.Issuer,
			AuthorizationEndpoint: authMeta.AuthorizationEndpoint,
			TokenEndpoint:         authMeta.TokenEndpoint,
			RegistrationEndpoint:  authMeta.RegistrationEndpoint,
			ClientID:              reg.ClientID,
			ClientSecret:          reg.ClientSecret,
		}
		if manual {
			// The authorization server will only redirect to loopback, which
			// reaches the user's own machine and never this process. Persist
			// the PKCE verifier so a later turn can redeem the code the user
			// pastes back.
			state, err := generateState()
			if err != nil {
				return nil, fmt.Errorf("generate oauth state: %w", err)
			}
			authURL, codeVerifier := discovery.BuildAuthURLWithPKCE(discovery.AuthURLParams{
				Meta: authMeta, Reg: reg, State: state,
				RedirectURI: redirectURI, MCPURL: resolvedURL,
			})
			authData.PendingExchange = &remotemcpstore.PendingExchange{
				CodeVerifier: codeVerifier,
				State:        state,
				RedirectURI:  redirectURI,
				StartedAt:    time.Now(),
			}
			if err := deps.Manager.SetRemoteMCPAuth(ctx, a.Name, authData); err != nil {
				return nil, fmt.Errorf("store auth metadata: %w", err)
			}

			slog.Info("remote MCP requires manual OAuth paste", "name", a.Name, "redirect_uri", redirectURI)

			result := buildAddResponse(entry, "pending_manual_auth", fmt.Sprintf(
				"This server only redirects to a loopback address, so its callback cannot reach tclaw. "+
					"Send the authorization URL to the user and tell them: after approving, the browser will "+
					"land on a %s page that FAILS TO LOAD — that is expected. They must copy the full URL from "+
					"the address bar and send it back. Then call remote_mcp_auth_complete with name=%q and that "+
					"URL. Do NOT use remote_mcp_auth_wait for this server; no callback will ever arrive.",
				redirectURI, a.Name))
			result["auth_url"] = authURL
			result["redirect_uri"] = redirectURI
			return json.Marshal(result)
		}

		if err := deps.Manager.SetRemoteMCPAuth(ctx, a.Name, authData); err != nil {
			return nil, fmt.Errorf("store auth metadata: %w", err)
		}

		// Build PKCE auth URL and create the pending flow.
		_, codeVerifier := discovery.BuildAuthURLWithPKCE(discovery.AuthURLParams{
			Meta: authMeta, Reg: reg, RedirectURI: redirectURI, MCPURL: resolvedURL,
		})

		flow := &pendingRemoteMCPFlow{
			name:          a.Name,
			mcpURL:        resolvedURL,
			authMeta:      authMeta,
			clientReg:     reg,
			manager:       deps.Manager,
			configUpdater: deps.ConfigUpdater,
			codeVerifier:  codeVerifier,
			done:          make(chan struct{}),
		}

		// Register with the callback server — it generates the state param
		// and will call flow.Complete/Fail when the callback arrives.
		state, err := deps.Callback.RegisterFlow(flow)
		if err != nil {
			return nil, fmt.Errorf("register oauth flow: %w", err)
		}

		// Rebuild the auth URL with the actual state token, reusing the
		// verifier already handed to the flow so challenge and verifier stay
		// in step.
		authURL, _ := discovery.BuildAuthURLWithPKCE(discovery.AuthURLParams{
			Meta: authMeta, Reg: reg, State: state, RedirectURI: redirectURI,
			MCPURL: resolvedURL, CodeVerifier: codeVerifier,
		})

		result := buildAddResponse(entry, "pending_auth",
			fmt.Sprintf("Send this authorization URL to the user. After they authorize, use remote_mcp_auth_wait with name=%q to confirm completion. Once authorized, the remote MCP's tools will be available on the next message.", a.Name))
		result["auth_url"] = authURL
		return json.Marshal(result)
	}
}

type chooseRedirectParams struct {
	authMeta    *discovery.AuthMetadata
	reg         *discovery.ClientRegistration
	mcpURL      string
	hostedURL   string
	forceManual bool
	opts        []discovery.DiscoverAuthOption
}

// chooseRedirectURI decides whether this authorization server can redirect
// back to tclaw's hosted callback, falling back to a loopback address the user
// relays by hand when it cannot.
//
// The probe uses a throwaway state and PKCE verifier: it never reaches the
// point of issuing a code, so nothing it builds needs to be kept.
//
// A probe that fails to reach the server is NOT treated as rejection — the
// hosted callback is kept, because downgrading a working server to a manual
// paste on a transient network error would be worse than the error itself.
func chooseRedirectURI(ctx context.Context, p chooseRedirectParams) (redirectURI string, manual bool, err error) {
	if p.forceManual {
		return manualRedirectURI, true, nil
	}

	probeURL, _ := discovery.BuildAuthURLWithPKCE(discovery.AuthURLParams{
		Meta: p.authMeta, Reg: p.reg, State: "probe",
		RedirectURI: p.hostedURL, MCPURL: p.mcpURL,
	})

	accepted, probeErr := discovery.RedirectURIAccepted(ctx, probeURL, p.opts...)
	switch {
	case probeErr != nil:
		slog.Warn("could not probe authorize endpoint, assuming hosted callback works",
			"err", probeErr)
		return p.hostedURL, false, nil
	case accepted:
		return p.hostedURL, false, nil
	}

	// The hosted callback was refused. A loopback address is the redirect
	// such servers do allow; verify before committing the user to a flow
	// that would dead-end anyway.
	loopbackProbeURL, _ := discovery.BuildAuthURLWithPKCE(discovery.AuthURLParams{
		Meta: p.authMeta, Reg: p.reg, State: "probe",
		RedirectURI: manualRedirectURI, MCPURL: p.mcpURL,
	})
	loopbackOK, probeErr := discovery.RedirectURIAccepted(ctx, loopbackProbeURL, p.opts...)
	if probeErr != nil {
		return "", false, fmt.Errorf("hosted callback %s was rejected and the loopback fallback could not be probed: %w", p.hostedURL, probeErr)
	}
	if !loopbackOK {
		return "", false, fmt.Errorf("authorization server rejected both tclaw's callback (%s) and a loopback redirect (%s) — it likely requires a redirect URI registered out of band", p.hostedURL, manualRedirectURI)
	}

	return manualRedirectURI, true, nil
}

// generateState creates the OAuth CSRF state token for a manual flow. The
// hosted flow gets its state from the callback server, which also tracks it;
// a manual flow has no callback server involved, so it mints and stores its own.
func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// buildAddResponse assembles a remote_mcp_add response using urlResponseFields
// so every exit path emits the same URL-handling contract (host always, url
// only when non-sensitive, url_is_secret flag in both cases).
func buildAddResponse(entry *remotemcpstore.RemoteMCP, status, message string) map[string]any {
	result := urlResponseFields(entry.URL, entry.URLSensitive)
	result["name"] = entry.Name
	result["status"] = status
	result["message"] = message
	// Surface the server's own usage guidance (session lifecycle, tool
	// conventions) so the agent can drive it correctly straight away.
	if entry.Instructions != "" {
		result["instructions"] = entry.Instructions
	}
	return result
}

// resolveURL returns the MCP URL, accepting either an inline value or a
// secret store key (exactly one). Using url_secret_key keeps URLs that
// contain a secret path segment out of chat history. Length is enforced here
// — scheme/host validation happens at the call site so the same rules apply
// to both inline and resolved URLs.
func resolveURL(ctx context.Context, store secret.Store, inline, secretKey string) (string, error) {
	switch {
	case inline != "" && secretKey != "":
		return "", fmt.Errorf("only one of url or url_secret_key may be provided")
	case inline != "":
		if len(inline) > maxMCPURLLength {
			return "", fmt.Errorf("url exceeds %d characters", maxMCPURLLength)
		}
		return inline, nil
	case secretKey != "":
		if store == nil {
			return "", fmt.Errorf("url_secret_key requires a configured secret store (not available in this context)")
		}
		value, err := store.Get(ctx, secretKey)
		if err != nil {
			return "", fmt.Errorf("read url secret %q: %w", secretKey, err)
		}
		if value == "" {
			return "", fmt.Errorf("url secret %q is unset — request it via secret_form_request first", secretKey)
		}
		if len(value) > maxMCPURLLength {
			return "", fmt.Errorf("resolved url exceeds %d characters", maxMCPURLLength)
		}
		return value, nil
	default:
		return "", fmt.Errorf("url or url_secret_key is required")
	}
}

// resolveHeaderSecretKeys looks up each secret store key and returns the
// corresponding header value map. Returns an error if the SecretStore is
// unavailable, any key is empty/malformed, or any referenced secret is
// missing. Secret values themselves are NEVER echoed in error messages or
// logs — only header names and key identifiers.
func resolveHeaderSecretKeys(ctx context.Context, store secret.Store, keys map[string]string) (map[string]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	if store == nil {
		return nil, fmt.Errorf("header_secret_keys requires a configured secret store (not available in this context)")
	}
	resolved := make(map[string]string, len(keys))
	for header, key := range keys {
		if key == "" {
			return nil, fmt.Errorf("header_secret_keys[%q]: secret store key is empty", header)
		}
		value, err := store.Get(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("header_secret_keys[%q]: read secret %q: %w", header, key, err)
		}
		if value == "" {
			return nil, fmt.Errorf("header_secret_keys[%q]: secret %q is unset — request it via secret_form_request first", header, key)
		}
		resolved[header] = value
	}
	return resolved, nil
}

// mergeHeaderMaps combines inline headers with secret-resolved headers. A
// header name appearing in both maps is an error — the caller should use one
// source per header, not both.
func mergeHeaderMaps(inline, resolved map[string]string) (map[string]string, error) {
	if len(inline) == 0 && len(resolved) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(inline)+len(resolved))
	for k, v := range inline {
		out[k] = v
	}
	for k, v := range resolved {
		if _, exists := out[k]; exists {
			return nil, fmt.Errorf("header %q is set both inline and via header_secret_keys — choose one source", k)
		}
		out[k] = v
	}
	return out, nil
}

func validateHeaders(headers map[string]string) error {
	if len(headers) == 0 {
		return nil
	}
	if len(headers) > maxHeaders {
		return fmt.Errorf("too many headers: max %d, got %d", maxHeaders, len(headers))
	}
	for name, value := range headers {
		if name == "" || len(name) > maxHeaderNameLength || !headerNamePattern.MatchString(name) {
			return fmt.Errorf("invalid header name %q: must be a valid HTTP field name under %d chars", name, maxHeaderNameLength)
		}
		if value == "" || len(value) > maxHeaderValueLength {
			return fmt.Errorf("invalid header value for %q: must be non-empty and under %d chars", name, maxHeaderValueLength)
		}
		// Reject CR/LF and other control chars to prevent header injection.
		for _, r := range value {
			if r < 0x20 && r != '\t' {
				return fmt.Errorf("invalid header value for %q: contains control character", name)
			}
			if r == 0x7f {
				return fmt.Errorf("invalid header value for %q: contains DEL character", name)
			}
		}
	}
	return nil
}

// listToolsOpts translates Deps into functional options for
// discovery.ListTools. Keeps the tool call site cluttered with a single
// helper rather than conditional WithHTTPClient branches.
func listToolsOpts(deps Deps) []discovery.ListToolsOption {
	if deps.HTTPClient == nil {
		return nil
	}
	return []discovery.ListToolsOption{discovery.WithHTTPClient(deps.HTTPClient)}
}

// discoverAuthOpts translates Deps into functional options for
// discovery.DiscoverAuth, mirroring listToolsOpts.
func discoverAuthOpts(deps Deps) []discovery.DiscoverAuthOption {
	if deps.HTTPClient == nil {
		return nil
	}
	return []discovery.DiscoverAuthOption{discovery.WithDiscoverAuthHTTPClient(deps.HTTPClient)}
}

// redactURL returns a URL suitable for logging — preserves scheme and host but
// drops the path (which may contain a secret, e.g. an unguessable path segment).
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<redacted>"
	}
	return u.Scheme + "://" + u.Host
}

// urlResponseFields returns the fields a tool response should include for a
// stored remote MCP. "host" is always present (scheme+host only). "url" is
// present only when the URL was non-sensitive (inline-registered). The
// url_is_secret flag gives callers an unambiguous signal; the invariant is:
//   - url_is_secret == false  ↔  "url" populated with full URL
//   - url_is_secret == true   ↔  "url" omitted (the caller cannot see the full URL)
//
// This prevents the agent from ever receiving a sensitive URL via tool output
// and from confusing a redacted URL with a real one.
func urlResponseFields(storedURL string, sensitive bool) map[string]any {
	fields := map[string]any{
		"host":          redactURL(storedURL),
		"url_is_secret": sensitive,
	}
	if !sensitive {
		fields["url"] = storedURL
	}
	return fields
}
