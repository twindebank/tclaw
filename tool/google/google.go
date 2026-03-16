package google

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"tclaw/connection"
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
	handler.Register(defs[2], calendarListHandler(connMap))
	handler.Register(defs[3], calendarCreateHandler(connMap))
	handler.Register(defs[4], workspaceHandler(connMap))
	handler.Register(defs[5], schemaHandler(connMap))
}

// UnregisterTools removes the Google Workspace tools from the handler.
func UnregisterTools(handler *mcp.Handler) {
	handler.Unregister("google_gmail_list")
	handler.Unregister("google_gmail_read")
	handler.Unregister("google_calendar_list")
	handler.Unregister("google_calendar_create")
	handler.Unregister("google_workspace")
	handler.Unregister("google_workspace_schema")
}

// resolveDeps looks up the Deps for a connection ID from the tool args.
func resolveDeps(connMap map[connection.ConnectionID]Deps, connIDStr string) (Deps, error) {
	return providerutil.ResolveDeps(connMap, connIDStr)
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
			home + "/.nvm/versions/node/*/bin/gws", // nvm
			"/usr/local/bin/gws",                   // system npm
			home + "/.local/bin/gws",               // user-local
			home + "/.npm-global/bin/gws",          // npm prefix
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
	return providerutil.AccessToken(ctx, deps)
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
		slog.Error("gws command failed", "args", strings.Join(args, " "), "error", err, "output", string(output))
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
		slog.Error("gws command failed", "args", strings.Join(args, " "), "error", err, "output", string(output))
		return "", fmt.Errorf("gws %s: %s", strings.Join(args, " "), string(output))
	}

	return string(output), nil
}
