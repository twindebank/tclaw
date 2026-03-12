package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	protocolVersion = "2025-03-26"
	serverName      = "tclaw"
	serverVersion   = "0.1.0"
)

// Server is an HTTP-based MCP server implementing the JSON-RPC protocol.
// It handles initialize, tools/list, and tools/call methods.
type Server struct {
	handler  *Handler
	listener net.Listener
	srv      *http.Server

	mu      sync.Mutex
	running bool
}

// NewServer creates an MCP server with the given tool handler.
// It does not start listening until Start is called.
func NewServer(handler *Handler) *Server {
	return &Server{handler: handler}
}

// Start begins serving on the given address (e.g. "127.0.0.1:0" for random port).
// Returns the actual address the server is listening on.
func (s *Server) Start(addr string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return s.listener.Addr().String(), nil
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}
	s.listener = ln

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	// Health check for debugging.
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	s.srv = &http.Server{Handler: mux}
	s.running = true

	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("mcp server error", "err", err)
		}
	}()

	slog.Info("mcp server started", "addr", ln.Addr().String())
	return ln.Addr().String(), nil
}

// Addr returns the address the server is listening on, or "" if not running.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}
	s.running = false
	return s.srv.Shutdown(ctx)
}

// Handler returns the underlying tool handler for registration.
func (s *Server) Handler() *Handler {
	return s.handler
}

// JSON-RPC types

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // may be absent for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// maxRequestBodySize limits MCP request bodies to 1 MiB to prevent
// a malicious or buggy agent from sending huge payloads.
const maxRequestBodySize = 1 << 20

// handleMCP processes MCP JSON-RPC requests. Supports both single requests
// and batched arrays (required by the MCP protocol).
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSONRPCError(w, nil, -32700, "parse error")
		return
	}

	// Determine if this is a batch (array) or single request.
	trimmed := json.RawMessage(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []jsonRPCRequest
		if err := json.Unmarshal(raw, &batch); err != nil {
			writeJSONRPCError(w, nil, -32700, "parse error")
			return
		}
		var responses []jsonRPCResponse
		for _, req := range batch {
			if rsp := s.dispatch(r.Context(), req); rsp != nil {
				responses = append(responses, *rsp)
			}
		}
		if len(responses) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(responses)
		return
	}

	var req jsonRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeJSONRPCError(w, nil, -32700, "parse error")
		return
	}

	rsp := s.dispatch(r.Context(), req)
	if rsp == nil {
		// Notification — no response.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rsp)
}

// dispatch routes a single JSON-RPC request to the appropriate handler.
// Returns nil for notifications (no ID).
func (s *Server) dispatch(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)

	case "notifications/initialized":
		// Notification — no response.
		return nil

	case "tools/list":
		return s.handleToolsList(req)

	case "tools/call":
		return s.handleToolsCall(ctx, req)

	default:
		if isNotification {
			return nil
		}
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func (s *Server) handleInitialize(req jsonRPCRequest) *jsonRPCResponse {
	result := map[string]any{
		"protocolVersion": protocolVersion,
		"serverInfo": map[string]string{
			"name":    serverName,
			"version": serverVersion,
		},
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
	}
	data, _ := json.Marshal(result)
	return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
}

func (s *Server) handleToolsList(req jsonRPCRequest) *jsonRPCResponse {
	tools := s.handler.ListTools()
	result := map[string]any{"tools": tools}
	data, _ := json.Marshal(result)
	return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolsCall(ctx context.Context, req jsonRPCRequest) *jsonRPCResponse {
	var params toolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32602, Message: "invalid params"},
		}
	}

	start := time.Now()
	result, err := s.handler.Call(ctx, params.Name, params.Arguments)
	duration := time.Since(start)

	if err != nil {
		slog.Warn("mcp tool call failed",
			"tool", params.Name,
			"duration_ms", duration.Milliseconds(),
			"error", err.Error(),
		)
		// MCP protocol: tool errors are returned as content with isError=true,
		// not as JSON-RPC errors. This lets Claude see and react to the error.
		errContent := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": err.Error()},
			},
			"isError": true,
		}
		data, _ := json.Marshal(errContent)
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
	}

	slog.Info("mcp tool call",
		"tool", params.Name,
		"duration_ms", duration.Milliseconds(),
	)

	// Wrap raw result in MCP content format.
	var resultText string
	if result != nil {
		resultText = string(result)
	} else {
		resultText = "OK"
	}
	content := map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": resultText},
		},
	}
	data, _ := json.Marshal(content)
	return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
}

func writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: message},
	})
}
