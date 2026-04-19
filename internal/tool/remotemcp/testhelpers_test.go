package remotemcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeMCPServer returns an httptest.NewTLSServer that speaks just enough of
// the MCP protocol (initialize + tools/list) to satisfy discovery.ListTools.
// Each tool in toolNames is exposed as {"name": <n>}. The server's Client
// (which trusts the self-signed cert) is also returned via server.Client()
// so tests can plumb it into remotemcp.Deps.HTTPClient.
func fakeMCPServer(t *testing.T, toolNames []string) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(fakeMCPHandler(toolNames, nil)))
	t.Cleanup(server.Close)
	return server
}

// withRecordedHeaders wraps fakeMCPServer so tests can assert what headers
// landed on each request. The returned snapshot function returns a clone of
// every request's Header at time of snapshot.
func withRecordedHeaders(t *testing.T, toolNames []string) (*httptest.Server, func() []http.Header) {
	t.Helper()
	var received []http.Header
	record := func(h http.Header) { received = append(received, h) }
	server := httptest.NewTLSServer(http.HandlerFunc(fakeMCPHandler(toolNames, record)))
	t.Cleanup(server.Close)
	return server, func() []http.Header { return received }
}

// fakeMCPHandler is the raw HTTP handler body for a minimal MCP server that
// replies to initialize and tools/list. Other methods 404. onRequest (if
// non-nil) is invoked with a clone of the request headers before dispatch.
func fakeMCPHandler(toolNames []string, onRequest func(http.Header)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if onRequest != nil {
			onRequest(r.Header.Clone())
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": "2025-06-18",
					"serverInfo":      map[string]string{"name": "fake", "version": "0.0.0"},
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			})
		case "tools/list":
			tools := make([]map[string]any, len(toolNames))
			for i, n := range toolNames {
				tools[i] = map[string]any{"name": n}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"tools": tools},
			})
		default:
			http.Error(w, "unknown method", http.StatusNotFound)
		}
	}
}
