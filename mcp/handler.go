package mcp

import (
	"context"
	"encoding/json"
)

// ToolHandler processes a tools/call request and returns the result content.
type ToolHandler func(ctx context.Context, args json.RawMessage) (json.RawMessage, error)

// ToolDef describes a single MCP tool for the tools/list response.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"` // JSON Schema object
}

// toolEntry pairs a definition with its handler.
type toolEntry struct {
	def     ToolDef
	handler ToolHandler
}

// Handler is the MCP tool registry and dispatcher.
type Handler struct {
	tools map[string]*toolEntry
}

// NewHandler creates an empty tool handler.
func NewHandler() *Handler {
	return &Handler{tools: make(map[string]*toolEntry)}
}

// Register adds a tool. Overwrites any existing tool with the same name.
func (h *Handler) Register(def ToolDef, handler ToolHandler) {
	h.tools[def.Name] = &toolEntry{def: def, handler: handler}
}

// Unregister removes a tool by name.
func (h *Handler) Unregister(name string) {
	delete(h.tools, name)
}

// ListTools returns definitions for all registered tools.
func (h *Handler) ListTools() []ToolDef {
	defs := make([]ToolDef, 0, len(h.tools))
	for _, entry := range h.tools {
		defs = append(defs, entry.def)
	}
	return defs
}

// Call dispatches a tools/call request to the matching handler.
// Returns the tool handler's result or an error if the tool is not found.
func (h *Handler) Call(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	entry, ok := h.tools[name]
	if !ok {
		return nil, &ToolNotFoundError{Name: name}
	}
	return entry.handler(ctx, args)
}

// ToolNotFoundError indicates a tools/call for an unregistered tool.
type ToolNotFoundError struct {
	Name string
}

func (e *ToolNotFoundError) Error() string {
	return "tool not found: " + e.Name
}
