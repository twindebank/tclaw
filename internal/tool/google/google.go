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
// Call this each time a Google credential set is added or removed. memoryDir
// is the user's sandboxed memory directory — google_workspace uses it as the
// gws subprocess's working directory so downloaded files (e.g. Drive binaries)
// land somewhere the agent sandbox can read, and so -a attachment paths already
// living under the memory dir pass gws's own "must resolve under CWD" check;
// empty disables this (downloads fall back to the process's own CWD and are
// unreachable by the agent, and absolute attachment paths are likely rejected).
func RegisterTools(handler *mcp.Handler, depsMap map[credential.CredentialSetID]Deps, memoryDir string) {
	setIDs := make([]credential.CredentialSetID, 0, len(depsMap))
	for id := range depsMap {
		setIDs = append(setIDs, id)
	}

	defs := ToolDefs(setIDs)
	handler.Register(defs[0], gmailListHandler(depsMap))
	handler.Register(defs[1], gmailReadHandler(depsMap))
	handler.Register(defs[2], gmailSendHandler(depsMap))
	handler.Register(defs[3], gmailForwardHandler(depsMap))
	handler.Register(defs[4], gmailModifyHandler(depsMap))
	handler.Register(defs[5], calendarListHandler(depsMap))
	handler.Register(defs[6], calendarCreateHandler(depsMap))
	handler.Register(defs[7], calendarUpdateHandler(depsMap))
	handler.Register(defs[8], workspaceHandler(depsMap, memoryDir))
	handler.Register(defs[9], schemaHandler(depsMap))
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

// gwsRunner runs a typed gws command against a credential set's Deps and returns
// the raw JSON output. runGWS is the production implementation; tests inject a
// stub (see the notifier's run field) to serve canned Gmail responses.
type gwsRunner = func(ctx context.Context, deps Deps, cmd gws.Command) (json.RawMessage, error)

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
