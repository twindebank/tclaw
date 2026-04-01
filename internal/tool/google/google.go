package google

import (
	"context"
	"encoding/json"

	"tclaw/internal/credential"
	"tclaw/internal/gws"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/providerutil"
)

// Deps holds dependencies for a single Google Workspace credential set.
type Deps = providerutil.Deps

// RegisterTools registers (or re-registers) the Google Workspace tools
// with handlers that resolve the credential set dynamically from depsMap.
// Call this each time a Google credential set is added or removed.
func RegisterTools(handler *mcp.Handler, depsMap map[credential.CredentialSetID]Deps) {
	setIDs := make([]credential.CredentialSetID, 0, len(depsMap))
	for id := range depsMap {
		setIDs = append(setIDs, id)
	}

	defs := ToolDefs(setIDs)
	handler.Register(defs[0], gmailListHandler(depsMap))
	handler.Register(defs[1], gmailReadHandler(depsMap))
	handler.Register(defs[2], gmailSendHandler(depsMap))
	handler.Register(defs[3], calendarListHandler(depsMap))
	handler.Register(defs[4], calendarCreateHandler(depsMap))
	handler.Register(defs[5], workspaceHandler(depsMap))
	handler.Register(defs[6], schemaHandler(depsMap))
}

// UnregisterTools removes the Google Workspace tools from the handler.
func UnregisterTools(handler *mcp.Handler) {
	for _, name := range ToolNames() {
		handler.Unregister(name)
	}
}

// resolveDeps looks up the Deps for a credential set ID from the tool args.
func resolveDeps(depsMap map[credential.CredentialSetID]Deps, idStr string) (Deps, error) {
	return providerutil.ResolveDeps(depsMap, idStr)
}

// accessToken gets a valid access token for the credential set, refreshing if needed.
func accessToken(ctx context.Context, deps Deps) (string, error) {
	return providerutil.AccessToken(ctx, deps)
}

// runGWS executes a typed gws command with the credential set's access token.
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
