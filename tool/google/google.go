package google

import (
	"context"
	"encoding/json"

	"tclaw/connection"
	"tclaw/gws"
	"tclaw/mcp"
	"tclaw/tool/providerutil"
)

// Deps holds dependencies for a single Google Workspace connection.
type Deps = providerutil.Deps

// RegisterTools registers (or re-registers) the Google Workspace tools
// with handlers that resolve the connection dynamically from connMap.
// Call this each time a Google connection is added or removed.
func RegisterTools(handler *mcp.Handler, connMap map[connection.ConnectionID]Deps) {
	connIDs := make([]connection.ConnectionID, 0, len(connMap))
	for id := range connMap {
		connIDs = append(connIDs, id)
	}

	defs := ToolDefs(connIDs)
	handler.Register(defs[0], gmailListHandler(connMap))
	handler.Register(defs[1], gmailReadHandler(connMap))
	handler.Register(defs[2], gmailSendHandler(connMap))
	handler.Register(defs[3], calendarListHandler(connMap))
	handler.Register(defs[4], calendarCreateHandler(connMap))
	handler.Register(defs[5], workspaceHandler(connMap))
	handler.Register(defs[6], schemaHandler(connMap))
}

// UnregisterTools removes the Google Workspace tools from the handler.
func UnregisterTools(handler *mcp.Handler) {
	handler.Unregister("google_gmail_list")
	handler.Unregister("google_gmail_read")
	handler.Unregister("google_gmail_send")
	handler.Unregister("google_calendar_list")
	handler.Unregister("google_calendar_create")
	handler.Unregister("google_workspace")
	handler.Unregister("google_workspace_schema")
}

// resolveDeps looks up the Deps for a connection ID from the tool args.
func resolveDeps(connMap map[connection.ConnectionID]Deps, connIDStr string) (Deps, error) {
	return providerutil.ResolveDeps(connMap, connIDStr)
}

// accessToken gets a valid access token for the connection, refreshing if needed.
func accessToken(ctx context.Context, deps Deps) (string, error) {
	return providerutil.AccessToken(ctx, deps)
}

// runGWS executes a typed gws command with the connection's access token.
// Returns the raw JSON output.
func runGWS(ctx context.Context, deps Deps, cmd gws.Command) (json.RawMessage, error) {
	token, err := accessToken(ctx, deps)
	if err != nil {
		return nil, err
	}
	return gws.Run(ctx, token, cmd)
}

// runGWSRaw executes a typed gws command that may not return JSON (e.g. schema).
func runGWSRaw(ctx context.Context, deps Deps, cmd gws.Command) (string, error) {
	token, err := accessToken(ctx, deps)
	if err != nil {
		return "", err
	}
	return gws.RunRaw(ctx, token, cmd)
}
