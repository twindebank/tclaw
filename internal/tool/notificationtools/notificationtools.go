package notificationtools

import (
	"tclaw/internal/mcp"
	"tclaw/internal/notification"
)

const (
	ToolTypes       = "notification_types"
	ToolSubscribe   = "notification_subscribe"
	ToolUnsubscribe = "notification_unsubscribe"
	ToolList        = "notification_list"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{ToolTypes, ToolSubscribe, ToolUnsubscribe, ToolList}
}

// Deps holds dependencies for notification management tools.
type Deps struct {
	Manager *notification.Manager
}

// RegisterTools adds notification management tools to the MCP handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(typesDef(), typesHandler(deps))
	handler.Register(subscribeDef(), subscribeHandler(deps))
	handler.Register(unsubscribeDef(), unsubscribeHandler(deps))
	handler.Register(listDef(), listHandler(deps))
}
