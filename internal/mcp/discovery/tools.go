package discovery

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

// ToolsListResult is what the MCP initialize + tools/list handshake discovered
// about a server.
type ToolsListResult struct {
	// ToolNames are the tool names the server exposed, in the order listed.
	ToolNames []string

	// Instructions is the server's InitializeResult.instructions — free-form
	// guidance on how to use the server and its features (e.g. session-lifecycle
	// notes). Empty if the server set none. tclaw surfaces this to the agent so
	// it knows how to drive the server rather than guessing.
	Instructions string
}

// ListTools performs the standard MCP initialize + tools/list handshake over
// HTTP and returns the exposed tool names plus the server's self-description.
// Used at remote_mcp_add time so tclaw can cache the list and expand
// tool-permission globs against real tool names (the Claude CLI's
// --allowedTools does not honour wildcards for MCP tools) and surface the
// server's usage instructions to the agent.
//
// headers are added to every request — used for auth layers that sit in front
// of the MCP server (e.g. Cloudflare Access service tokens).
func ListTools(ctx context.Context, mcpURL string, headers map[string]string, opts ...ListToolsOption) (ToolsListResult, error) {
	cfg := listToolsConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	client := cfg.client
	if client == nil {
		client = safeClient
		if err := validateExternalURL(mcpURL); err != nil {
			return ToolsListResult{}, fmt.Errorf("unsafe MCP URL: %w", err)
		}
	}

	// Initialize the MCP session. FastMCP's HTTP transport is stateless by
	// default, but the spec still requires the initialize handshake before
	// other methods. Servers that operate statefully set Mcp-Session-Id in
	// the initialize response and expect it echoed on subsequent requests.
	init, err := mcpInitialize(ctx, client, mcpURL, headers)
	if err != nil {
		return ToolsListResult{}, fmt.Errorf("mcp initialize: %w", err)
	}

	// Call tools/list. Response shape: {"result": {"tools": [{"name": "..."}, ...]}}
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/list",
		Params:  json.RawMessage(`{}`),
	}
	raw, err := postMCP(ctx, client, mcpURL, headers, init.sessionID, req)
	if err != nil {
		return ToolsListResult{}, fmt.Errorf("tools/list: %w", err)
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
		return ToolsListResult{}, fmt.Errorf("parse tools/list response: %w", err)
	}
	if parsed.Error != nil {
		return ToolsListResult{}, fmt.Errorf("tools/list rpc error %d: %s", parsed.Error.Code, parsed.Error.Message)
	}

	names := make([]string, len(parsed.Result.Tools))
	for i, t := range parsed.Result.Tools {
		if t.Name == "" {
			return ToolsListResult{}, fmt.Errorf("tools/list returned a tool with empty name at index %d", i)
		}
		names[i] = t.Name
	}
	return ToolsListResult{ToolNames: names, Instructions: init.instructions}, nil
}

// initializeResult carries what the MCP initialize handshake surfaced.
type initializeResult struct {
	// sessionID is the Mcp-Session-Id the server assigned, echoed on subsequent
	// requests. Empty for stateless servers — that's fine, callers just omit the
	// header.
	sessionID string

	// instructions is the server's InitializeResult.instructions — free-form
	// guidance on how to use the server. Empty if the server set none.
	instructions string
}

// mcpInitialize sends the MCP initialize request and returns the session ID
// (if the server set one) along with the server's instructions field.
func mcpInitialize(ctx context.Context, client *http.Client, mcpURL string, headers map[string]string) (initializeResult, error) {
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
		return initializeResult{}, fmt.Errorf("marshal initialize: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(body))
	if err != nil {
		return initializeResult{}, fmt.Errorf("create initialize request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return initializeResult{}, fmt.Errorf("send initialize: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return initializeResult{}, fmt.Errorf("read initialize response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Cap the body for diagnostics.
		preview := raw
		if len(preview) > 512 {
			preview = preview[:512]
		}
		return initializeResult{}, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
	}

	// Unwrap SSE framing if the server used the streamable-http transport, so we
	// can read the instructions field out of the initialize result body.
	payload := raw
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		payload, err = extractSSEMessage(raw)
		if err != nil {
			return initializeResult{}, fmt.Errorf("parse SSE initialize response: %w", err)
		}
	}

	result := initializeResult{sessionID: resp.Header.Get("Mcp-Session-Id")}

	// instructions are optional and best-effort: the session id already came
	// from the header and tools/list works without them, so a malformed body is
	// a warning, not a fatal error.
	var parsed struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &parsed); err != nil {
			slog.Warn("mcp initialize: could not parse response body for instructions", "err", err)
		} else {
			result.instructions = parsed.Result.Instructions
		}
	}
	return result, nil
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
