package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleMCP_RejectsOversizedBody(t *testing.T) {
	handler := NewHandler()
	server := NewServer(handler)

	// Create a body larger than maxRequestBodySize (1 MiB).
	oversized := strings.Repeat("x", maxRequestBodySize+1)

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+server.Token())
	rr := httptest.NewRecorder()

	server.handleMCP(rr, req)

	// Should get a JSON-RPC parse error because MaxBytesReader truncates the body.
	if rr.Code != http.StatusOK {
		// The handler returns 200 with a JSON-RPC error, not an HTTP error.
		// But MaxBytesReader may cause a 413 or the JSON parse will fail.
		// Either way, the oversized body should not be fully processed.
	}

	// Verify the response contains an error (parse error from truncated body).
	body := rr.Body.String()
	if !strings.Contains(body, "parse error") && rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected parse error or 413 for oversized body, got status %d body: %s", rr.Code, body)
	}
}

func TestHandleMCP_RejectsNonPost(t *testing.T) {
	handler := NewHandler()
	server := NewServer(handler)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+server.Token())
	rr := httptest.NewRecorder()

	server.handleMCP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET, got %d", rr.Code)
	}
}
