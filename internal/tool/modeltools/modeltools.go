package modeltools

import (
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
)

// storeKey is the state store key for the runtime model override.
const storeKey = "model_override"

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{ToolList, ToolGet, ToolSet}
}

// Deps holds dependencies for model management tools.
type Deps struct {
	Store store.Store
}

// RegisterTools adds model management tools to the MCP handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(modelListDef(), modelListHandler())
	handler.Register(modelGetDef(), modelGetHandler(deps))
	handler.Register(modelSetDef(), modelSetHandler(deps))
}
