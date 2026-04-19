package remotemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"

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
		if parsed.Scheme != "https" {
			return nil, fmt.Errorf("only HTTPS MCP server URLs are allowed")
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

		// Store the remote MCP entry.
		entry, err := deps.Manager.AddRemoteMCP(ctx, remotemcpstore.AddRemoteMCPParams{
			Name:         a.Name,
			URL:          resolvedURL,
			Channel:      a.Channel,
			URLSensitive: a.URLSecretKey != "",
		})
		if err != nil {
			return nil, fmt.Errorf("add remote mcp: %w", err)
		}

		if a.SkipAuthDiscovery {
			if len(mergedHeaders) > 0 {
				authData := &remotemcpstore.RemoteMCPAuth{StaticHeaders: mergedHeaders}
				if err := deps.Manager.SetRemoteMCPAuth(ctx, a.Name, authData); err != nil {
					return nil, fmt.Errorf("store static headers: %w", err)
				}
			}
			if updateErr := deps.ConfigUpdater(ctx); updateErr != nil {
				return nil, fmt.Errorf("remote MCP %q added but config update failed — tools won't be available until next restart: %w", a.Name, updateErr)
			}
			result := buildAddResponse(entry, "ready",
				fmt.Sprintf("Remote MCP %q added with %d static header(s) attached. Its tools will be available on the next message.", a.Name, len(mergedHeaders)))
			return json.Marshal(result)
		}

		slog.Info("discovering auth for remote MCP", "name", a.Name, "host", parsed.Host)

		// Discover whether OAuth is required.
		authMeta, err := discovery.DiscoverAuth(ctx, resolvedURL)
		if err != nil {
			slog.Warn("auth discovery failed, adding without auth", "name", a.Name, "err", err)
			if updateErr := deps.ConfigUpdater(ctx); updateErr != nil {
				return nil, fmt.Errorf("remote MCP %q added but config update failed — tools won't be available until next restart: %w", a.Name, updateErr)
			}
			result := buildAddResponse(entry, "ready",
				fmt.Sprintf("Remote MCP %q added (no auth or discovery failed). Its tools will be available on the next message.", a.Name))
			return json.Marshal(result)
		}

		// No auth needed — just add it and update the config.
		if authMeta == nil {
			if updateErr := deps.ConfigUpdater(ctx); updateErr != nil {
				return nil, fmt.Errorf("remote MCP %q added but config update failed — tools won't be available until next restart: %w", a.Name, updateErr)
			}
			result := buildAddResponse(entry, "ready",
				fmt.Sprintf("Remote MCP %q added (no auth required). Its tools will be available on the next message.", a.Name))
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
			reg, err = discovery.RegisterClient(ctx, authMeta, callbackURL)
			if err != nil {
				return nil, fmt.Errorf("dynamic client registration: %w", err)
			}
			slog.Info("registered OAuth client", "name", a.Name, "client_id", reg.ClientID)
		} else {
			return nil, fmt.Errorf("remote MCP %q requires OAuth but does not support dynamic client registration — manual client_id configuration not yet supported", a.Name)
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
		if err := deps.Manager.SetRemoteMCPAuth(ctx, a.Name, authData); err != nil {
			return nil, fmt.Errorf("store auth metadata: %w", err)
		}

		// Build PKCE auth URL and create the pending flow.
		_, codeVerifier := discovery.BuildAuthURLWithPKCE(authMeta, reg, "", callbackURL, resolvedURL)

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

		// Rebuild the auth URL with the actual state token.
		authURL, _ := discovery.BuildAuthURLWithPKCE(authMeta, reg, state, callbackURL, resolvedURL)

		result := buildAddResponse(entry, "pending_auth",
			fmt.Sprintf("Send this authorization URL to the user. After they authorize, use remote_mcp_auth_wait with name=%q to confirm completion. Once authorized, the remote MCP's tools will be available on the next message.", a.Name))
		result["auth_url"] = authURL
		return json.Marshal(result)
	}
}

// buildAddResponse assembles a remote_mcp_add response using urlResponseFields
// so every exit path emits the same URL-handling contract (host always, url
// only when non-sensitive, url_is_secret flag in both cases).
func buildAddResponse(entry *remotemcpstore.RemoteMCP, status, message string) map[string]any {
	result := urlResponseFields(entry.URL, entry.URLSensitive)
	result["name"] = entry.Name
	result["status"] = status
	result["message"] = message
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
