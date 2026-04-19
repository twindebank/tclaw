package discovery_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/mcp/discovery"
)

func TestListTools(t *testing.T) {
	t.Run("happy path returns tool names", func(t *testing.T) {
		server := newFakeMCPServer(t, fakeMCPOptions{
			Tools: []fakeTool{{Name: "ha_state_get"}, {Name: "ha_service_call"}},
		})
		names, err := discovery.ListTools(context.Background(), server.URL+"/mcp", nil, discovery.WithHTTPClient(server.Client()))
		require.NoError(t, err)
		require.Equal(t, []string{"ha_state_get", "ha_service_call"}, names)
	})

	t.Run("passes custom headers on initialize and tools/list", func(t *testing.T) {
		server := newFakeMCPServer(t, fakeMCPOptions{
			Tools: []fakeTool{{Name: "one"}},
		})
		headers := map[string]string{
			"CF-Access-Client-Id":     "client-id.access",
			"CF-Access-Client-Secret": "the-secret",
		}
		_, err := discovery.ListTools(context.Background(), server.URL+"/mcp", headers, discovery.WithHTTPClient(server.Client()))
		require.NoError(t, err)

		require.Equal(t, 2, server.RequestCount(), "expected initialize + tools/list")
		for i, req := range server.Requests() {
			require.Equal(t, "client-id.access", req.Header.Get("CF-Access-Client-Id"),
				"request %d missing client id", i)
			require.Equal(t, "the-secret", req.Header.Get("CF-Access-Client-Secret"),
				"request %d missing client secret", i)
		}
	})

	t.Run("unwraps SSE-framed responses", func(t *testing.T) {
		server := newFakeMCPServer(t, fakeMCPOptions{
			Tools:        []fakeTool{{Name: "sse_tool"}},
			UseSSEFrames: true,
		})
		names, err := discovery.ListTools(context.Background(), server.URL+"/mcp", nil, discovery.WithHTTPClient(server.Client()))
		require.NoError(t, err)
		require.Equal(t, []string{"sse_tool"}, names)
	})

	t.Run("propagates session id from initialize to tools/list", func(t *testing.T) {
		server := newFakeMCPServer(t, fakeMCPOptions{
			Tools:          []fakeTool{{Name: "stateful_tool"}},
			SessionID:      "sess-abc-123",
			RequireSession: true,
		})
		names, err := discovery.ListTools(context.Background(), server.URL+"/mcp", nil, discovery.WithHTTPClient(server.Client()))
		require.NoError(t, err)
		require.Equal(t, []string{"stateful_tool"}, names)
	})

	t.Run("surfaces rpc error from tools/list", func(t *testing.T) {
		server := newFakeMCPServer(t, fakeMCPOptions{
			ToolsListError: &rpcError{Code: -32603, Message: "internal error"},
		})
		_, err := discovery.ListTools(context.Background(), server.URL+"/mcp", nil, discovery.WithHTTPClient(server.Client()))
		require.Error(t, err)
		require.Contains(t, err.Error(), "internal error")
	})

	t.Run("surfaces non-2xx from initialize", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}))
		t.Cleanup(server.Close)
		_, err := discovery.ListTools(context.Background(), server.URL+"/mcp", nil, discovery.WithHTTPClient(server.Client()))
		require.Error(t, err)
		require.Contains(t, err.Error(), "403")
	})

	t.Run("rejects empty tool name from server", func(t *testing.T) {
		server := newFakeMCPServer(t, fakeMCPOptions{
			Tools: []fakeTool{{Name: ""}},
		})
		_, err := discovery.ListTools(context.Background(), server.URL+"/mcp", nil, discovery.WithHTTPClient(server.Client()))
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty name")
	})
}

// --- helpers ---

type fakeTool struct {
	Name string
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type fakeMCPOptions struct {
	Tools          []fakeTool
	UseSSEFrames   bool
	SessionID      string // if set, returned from initialize and required on tools/list
	RequireSession bool   // 400 if tools/list arrives without the session id
	ToolsListError *rpcError
}

type fakeMCPServer struct {
	*httptest.Server
	requests []*http.Request
}

func (s *fakeMCPServer) RequestCount() int         { return len(s.requests) }
func (s *fakeMCPServer) Requests() []*http.Request { return s.requests }

func newFakeMCPServer(t *testing.T, opts fakeMCPOptions) *fakeMCPServer {
	t.Helper()
	fs := &fakeMCPServer{}
	fs.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.requests = append(fs.requests, r.Clone(r.Context()))

		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "initialize":
			if opts.SessionID != "" {
				w.Header().Set("Mcp-Session-Id", opts.SessionID)
			}
			writeMCPResponse(w, opts.UseSSEFrames, mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": "2025-06-18",
					"serverInfo":      map[string]string{"name": "fake", "version": "0.0.0"},
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			})
		case "tools/list":
			if opts.RequireSession && r.Header.Get("Mcp-Session-Id") != opts.SessionID {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			if opts.ToolsListError != nil {
				writeMCPResponse(w, opts.UseSSEFrames, mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   opts.ToolsListError,
				})
				return
			}
			tools := make([]map[string]any, len(opts.Tools))
			for i, tool := range opts.Tools {
				tools[i] = map[string]any{"name": tool.Name}
			}
			writeMCPResponse(w, opts.UseSSEFrames, mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]any{"tools": tools},
			})
		default:
			http.Error(w, "unknown method", http.StatusNotFound)
		}
	}))
	t.Cleanup(fs.Close)
	return fs
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func writeMCPResponse(w http.ResponseWriter, sse bool, resp mcpResponse) {
	payload, _ := json.Marshal(resp)
	if sse {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message\ndata: "))
		w.Write(payload)
		w.Write([]byte("\n\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(payload)
}
