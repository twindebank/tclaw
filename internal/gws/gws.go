// Package gws provides typed constants and a runner for the Google Workspace
// CLI (gws). It mirrors the claudecli/ pattern — pure types + execution, no
// tool registration or MCP logic.
package gws

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
)

// Service identifies a Google Workspace API service.
type Service string

const (
	ServiceGmail    Service = "gmail"
	ServiceCalendar Service = "calendar"
	ServiceDrive    Service = "drive"
	ServiceDocs     Service = "documents"
	ServiceSheets   Service = "spreadsheets"
	ServiceSlides   Service = "presentations"
	ServiceTasks    Service = "tasks"
)

// Resource identifies an API resource within a service.
type Resource string

// Gmail resources.
const (
	ResourceUsers Resource = "users"
)

// Method identifies an API method on a resource.
type Method string

// Common methods shared across services.
const (
	MethodList   Method = "list"
	MethodGet    Method = "get"
	MethodInsert Method = "insert"
	MethodSend   Method = "send"
)

// SubResource identifies a nested resource (e.g. "messages" under "users").
type SubResource string

const (
	SubResourceMessages SubResource = "messages"
	SubResourceEvents   SubResource = "events"
)

// Command represents a fully-typed gws CLI invocation.
type Command struct {
	// Service + resource path: e.g. ["gmail", "users", "messages", "list"]
	Args []string

	// Params are URL/query parameters passed via --params as JSON.
	Params map[string]any

	// Body is the request body passed via --json as JSON.
	Body map[string]any
}

// Gmail constructs commands for the Gmail API.
var Gmail = gmailBuilder{}

type gmailBuilder struct{}

func (gmailBuilder) ListMessages(params map[string]any) Command {
	return Command{
		Args:   []string{"gmail", "users", "messages", "list"},
		Params: params,
	}
}

func (gmailBuilder) GetMessage(params map[string]any) Command {
	return Command{
		Args:   []string{"gmail", "users", "messages", "get"},
		Params: params,
	}
}

func (gmailBuilder) SendMessage(params map[string]any, body map[string]any) Command {
	return Command{
		Args:   []string{"gmail", "users", "messages", "send"},
		Params: params,
		Body:   body,
	}
}

// Calendar constructs commands for the Calendar API.
var Calendar = calendarBuilder{}

type calendarBuilder struct{}

func (calendarBuilder) ListEvents(params map[string]any) Command {
	return Command{
		Args:   []string{"calendar", "events", "list"},
		Params: params,
	}
}

func (calendarBuilder) InsertEvent(params map[string]any, body map[string]any) Command {
	return Command{
		Args:   []string{"calendar", "events", "insert"},
		Params: params,
		Body:   body,
	}
}

// Schema returns API schema documentation for the given dotted method name.
func Schema(method string) Command {
	return Command{
		Args: []string{"schema", method},
	}
}

// Raw constructs a command from a raw space-separated command string.
// Used by the generic google_workspace tool.
func Raw(command string, params, body string) Command {
	cmd := Command{
		Args: strings.Fields(command),
	}
	if params != "" {
		cmd.Params = map[string]any{"__raw": params}
	}
	if body != "" {
		cmd.Body = map[string]any{"__raw": body}
	}
	return cmd
}

// CLIArgs returns the full argument list for exec, including --params and --json flags.
func (c Command) CLIArgs() ([]string, error) {
	args := make([]string, len(c.Args))
	copy(args, c.Args)

	if len(c.Params) > 0 {
		paramsJSON, err := marshalMapOrRaw(c.Params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		args = append(args, "--params", string(paramsJSON))
	}

	if len(c.Body) > 0 {
		bodyJSON, err := marshalMapOrRaw(c.Body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		args = append(args, "--json", string(bodyJSON))
	}

	return args, nil
}

// marshalMapOrRaw marshals a map to JSON, or if the map contains a single
// "__raw" key, returns that value as pre-formatted JSON. This supports the
// Raw() constructor where the caller provides pre-serialized JSON strings.
func marshalMapOrRaw(m map[string]any) ([]byte, error) {
	if raw, ok := m["__raw"]; ok && len(m) == 1 {
		str, isString := raw.(string)
		if !isString {
			return nil, fmt.Errorf("__raw value must be a string, got %T", raw)
		}
		return []byte(str), nil
	}
	return json.Marshal(m)
}

// Run executes a gws command and returns the parsed JSON output.
func Run(ctx context.Context, token string, cmd Command) (json.RawMessage, error) {
	args, err := cmd.CLIArgs()
	if err != nil {
		return nil, err
	}

	binary := FindBinary()
	execCmd := exec.CommandContext(ctx, binary, args...)
	execCmd.Env = buildEnv(token)

	output, err := execCmd.CombinedOutput()
	if err != nil {
		slog.Error("gws command failed", "args", strings.Join(args, " "), "error", err, "output", string(output))
		return nil, fmt.Errorf("gws %s: %s", strings.Join(args, " "), string(output))
	}

	trimmed := strings.TrimSpace(string(output))
	if len(trimmed) == 0 {
		return json.RawMessage(`{"status": "ok"}`), nil
	}
	return json.RawMessage(trimmed), nil
}

// RunRaw executes a gws command that may not return JSON (e.g. schema, help).
func RunRaw(ctx context.Context, token string, cmd Command) (string, error) {
	args, err := cmd.CLIArgs()
	if err != nil {
		return "", err
	}

	binary := FindBinary()
	execCmd := exec.CommandContext(ctx, binary, args...)
	execCmd.Env = buildEnv(token)

	output, err := execCmd.CombinedOutput()
	if err != nil {
		slog.Error("gws command failed", "args", strings.Join(args, " "), "error", err, "output", string(output))
		return "", fmt.Errorf("gws %s: %s", strings.Join(args, " "), string(output))
	}

	return string(output), nil
}

// allowedEnvPrefixes mirrors the agent subprocess allowlist — only these env
// vars are forwarded to the gws CLI to avoid leaking secrets.
var allowedGWSEnvPrefixes = []string{
	"PATH", "TERM", "LANG", "LC_", "TMPDIR", "USER", "LOGNAME", "SHELL", "HOME", "XDG_", "TZ",
}

// buildEnv constructs a minimal environment for the gws subprocess using an
// allowlist, plus the Google Workspace CLI token.
func buildEnv(token string) []string {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		allowed := false
		for _, prefix := range allowedGWSEnvPrefixes {
			if key == prefix || strings.HasPrefix(key, prefix) {
				allowed = true
				break
			}
		}
		if allowed {
			env = append(env, kv)
		}
	}
	env = append(env, "GOOGLE_WORKSPACE_CLI_TOKEN="+token)
	return env
}

var (
	binaryOnce sync.Once
	binaryPath string
)

// FindBinary locates the gws binary. Checks PATH first, then common
// locations where npm/nvm install global packages.
func FindBinary() string {
	binaryOnce.Do(func() {
		if path, err := exec.LookPath("gws"); err == nil {
			binaryPath = path
			return
		}

		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			slog.Debug("failed to get home dir for gws binary search", "err", homeErr)
		}
		candidates := []string{
			home + "/.nvm/versions/node/*/bin/gws",
			"/usr/local/bin/gws",
			home + "/.local/bin/gws",
			home + "/.npm-global/bin/gws",
		}

		for _, pattern := range candidates {
			matches, globErr := filepath.Glob(pattern)
			if globErr != nil {
				slog.Debug("gws binary glob failed", "pattern", pattern, "err", globErr)
				continue
			}
			if len(matches) > 0 {
				// Latest version if nvm glob matches multiple.
				binaryPath = matches[len(matches)-1]
				return
			}
		}

		binaryPath = "gws"
	})
	return binaryPath
}
