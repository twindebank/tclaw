package google

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/provider"
)

// Deps holds dependencies for a single Google Workspace connection.
type Deps struct {
	ConnID   connection.ConnectionID
	Manager  *connection.Manager
	Provider *provider.Provider
}

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
	handler.Register(defs[1], workspaceHandler(connMap))
	handler.Register(defs[2], schemaHandler(connMap))
}

// UnregisterTools removes the Google Workspace tools from the handler.
func UnregisterTools(handler *mcp.Handler) {
	handler.Unregister("google_gmail_list")
	handler.Unregister("google_workspace")
	handler.Unregister("google_workspace_schema")
}

// resolveDeps looks up the Deps for a connection ID from the tool args.
func resolveDeps(connMap map[connection.ConnectionID]Deps, connIDStr string) (Deps, error) {
	connID := connection.ConnectionID(connIDStr)
	deps, ok := connMap[connID]
	if !ok {
		available := make([]string, 0, len(connMap))
		for id := range connMap {
			available = append(available, string(id))
		}
		return Deps{}, fmt.Errorf("unknown connection %q — available: %s", connIDStr, strings.Join(available, ", "))
	}
	return deps, nil
}

var (
	gwsBinaryOnce sync.Once
	gwsBinaryPath string
)

// findGWSBinary locates the gws binary. Checks PATH first, then common
// locations where npm/nvm install global packages.
func findGWSBinary() string {
	gwsBinaryOnce.Do(func() {
		// Try PATH first.
		if path, err := exec.LookPath("gws"); err == nil {
			gwsBinaryPath = path
			return
		}

		// Common global install locations for npm/nvm.
		home, _ := os.UserHomeDir()
		candidates := []string{
			home + "/.nvm/versions/node/*/bin/gws",  // nvm
			"/usr/local/bin/gws",                     // system npm
			home + "/.local/bin/gws",                 // user-local
			home + "/.npm-global/bin/gws",            // npm prefix
		}

		for _, pattern := range candidates {
			matches, _ := filepath.Glob(pattern)
			if len(matches) > 0 {
				gwsBinaryPath = matches[len(matches)-1] // latest version if nvm glob matches multiple
				return
			}
		}

		// Fall back to bare name — will fail at exec time with a clear error.
		gwsBinaryPath = "gws"
	})
	return gwsBinaryPath
}

// accessToken gets a valid access token for the connection, refreshing if needed.
func accessToken(ctx context.Context, deps Deps) (string, error) {
	refreshFn := func(ctx context.Context, refreshToken string) (*connection.Credentials, error) {
		return oauth.RefreshToken(ctx, deps.Provider.OAuth2, refreshToken)
	}
	creds, err := deps.Manager.RefreshIfNeeded(ctx, deps.ConnID, refreshFn)
	if err != nil {
		return "", fmt.Errorf("get credentials for %s: %w", deps.ConnID, err)
	}
	return creds.AccessToken, nil
}

// runGWS executes a gws command with the connection's access token injected via env var.
// Returns the raw output (typically JSON).
func runGWS(ctx context.Context, deps Deps, args ...string) (json.RawMessage, error) {
	token, err := accessToken(ctx, deps)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, findGWSBinary(), args...)
	cmd.Env = append(os.Environ(), "GOOGLE_WORKSPACE_CLI_TOKEN="+token)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gws %s: %s", strings.Join(args, " "), string(output))
	}

	// gws outputs JSON — return as raw message so the MCP response preserves structure.
	trimmed := strings.TrimSpace(string(output))
	if len(trimmed) == 0 {
		return json.RawMessage(`{"status": "ok"}`), nil
	}
	return json.RawMessage(trimmed), nil
}

// runGWSRaw executes a gws command that may not return JSON (e.g. schema, help).
func runGWSRaw(ctx context.Context, deps Deps, args ...string) (string, error) {
	token, err := accessToken(ctx, deps)
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, findGWSBinary(), args...)
	cmd.Env = append(os.Environ(), "GOOGLE_WORKSPACE_CLI_TOKEN="+token)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gws %s: %s", strings.Join(args, " "), string(output))
	}

	return string(output), nil
}
