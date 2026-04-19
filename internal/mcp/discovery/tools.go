package discovery

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ListToolsOption customises ListTools behaviour.
type ListToolsOption func(*listToolsConfig)

type listToolsConfig struct {
	client *http.Client
}

// WithHTTPClient overrides the default SSRF-safe HTTP client used for the
// tools/list handshake AND skips the SSRF URL validation (the caller is
// presumed to have pinned their own trust). Primarily for tests that want
// to talk to httptest.NewTLSServer instances on 127.0.0.1, but also usable
// by production callers who need specific TLS config or timeouts.
func WithHTTPClient(c *http.Client) ListToolsOption {
	return func(cfg *listToolsConfig) { cfg.client = c }
}

// ListTools fetches the tool names exposed by an MCP server by performing the
// standard MCP initialize + tools/list handshake over HTTP. Used at
// remote_mcp_add time so tclaw can cache the list and expand tool-permission
// globs against real tool names (the Claude CLI's --allowedTools does not
// honour wildcards for MCP tools).
//
// headers are added to every request — used for auth layers that sit in front
// of the MCP server (e.g. Cloudflare Access service tokens).
//
// Returns the tool names in the order the server listed them.
func ListTools(ctx context.Context, mcpURL string, headers map[string]string, opts ...ListToolsOption) ([]string, error) {
	cfg := listToolsConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	client := cfg.client
	if client == nil {
		client = safeClient
		if err := validateExternalURL(mcpURL); err != nil {
			return nil, fmt.Errorf("unsafe MCP URL: %w", err)
		}
	}

	// Initialize the MCP session. FastMCP's HTTP transport is stateless by
	// default, but the spec still requires the initialize handshake before
	// other methods. Servers that operate statefully set Mcp-Session-Id in
	// the initialize response and expect it echoed on subsequent requests.
	sessionID, err := mcpInitialize(ctx, client, mcpURL, headers)
	if err != nil {
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}

	// Call tools/list. Response shape: {"result": {"tools": [{"name": "..."}, ...]}}
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/list",
		Params:  json.RawMessage(`{}`),
	}
	raw, err := postMCP(ctx, client, mcpURL, headers, sessionID, req)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	var parsed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse tools/list response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("tools/list rpc error %d: %s", parsed.Error.Code, parsed.Error.Message)
	}

	names := make([]string, len(parsed.Result.Tools))
	for i, t := range parsed.Result.Tools {
		if t.Name == "" {
			return nil, fmt.Errorf("tools/list returned a tool with empty name at index %d", i)
		}
		names[i] = t.Name
	}
	return names, nil
}

// mcpInitialize sends the MCP initialize request and returns the session ID
// (if the server set one). Stateless servers return an empty session ID —
// that's fine, subsequent requests just omit the header.
func mcpInitialize(ctx context.Context, client *http.Client, mcpURL string, headers map[string]string) (string, error) {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params: mustMarshal(map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]string{"name": "tclaw", "version": "0.1.0"},
		}),
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal initialize: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create initialize request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("send initialize: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a capped amount of the body for diagnostics.
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
	}

	// Drain to allow connection reuse.
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBodyBytes))
	return resp.Header.Get("Mcp-Session-Id"), nil
}

// postMCP sends a JSON-RPC request to an MCP server and returns the decoded
// response body (unwrapping SSE framing if the server replied with
// text/event-stream). Meant for request/response methods — notifications
// don't return a body.
func postMCP(ctx context.Context, client *http.Client, mcpURL string, headers map[string]string, sessionID string, rpc jsonRPCRequest) ([]byte, error) {
	body, err := json.Marshal(rpc)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", sessionID)
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	// Unwrap SSE framing if present. Spec-compliant MCP servers using the
	// streamable-http transport (e.g. FastMCP) return
	// Content-Type: text/event-stream with the JSON-RPC response packed
	// inside a single data: line.
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		payload, err := extractSSEMessage(raw)
		if err != nil {
			return nil, fmt.Errorf("parse SSE response: %w", err)
		}
		return payload, nil
	}
	return raw, nil
}

// extractSSEMessage returns the first `data:` payload from an SSE stream.
// MCP JSON-RPC responses are always a single message per stream.
func extractSSEMessage(raw []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 4096), maxResponseBodyBytes)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			return []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("no data: line found in SSE response")
}
