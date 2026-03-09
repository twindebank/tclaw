package google

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/provider"
)

// Deps holds dependencies for Google Workspace tool handlers.
type Deps struct {
	ConnID   connection.ConnectionID
	Manager  *connection.Manager
	Provider *provider.Provider
	GWSPath  string // path to gws binary, defaults to "gws" (PATH lookup)
}

// RegisterTools adds the Google Workspace tools for a specific connection to the MCP handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	defs := ToolDefs(deps.ConnID)
	handler.Register(defs[0], workspaceHandler(deps))
	handler.Register(defs[1], schemaHandler(deps))
}

// resolveGWSPath returns the gws binary path, defaulting to "gws".
func resolveGWSPath(deps Deps) string {
	if deps.GWSPath != "" {
		return deps.GWSPath
	}
	return "gws"
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

	cmd := exec.CommandContext(ctx, resolveGWSPath(deps), args...)
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

	cmd := exec.CommandContext(ctx, resolveGWSPath(deps), args...)
	cmd.Env = append(os.Environ(), "GOOGLE_WORKSPACE_CLI_TOKEN="+token)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gws %s: %s", strings.Join(args, " "), string(output))
	}

	return string(output), nil
}
