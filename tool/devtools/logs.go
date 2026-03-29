package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tclaw/libraries/logbuffer"
	"tclaw/mcp"
)

const ToolLogs = "dev_logs"

func devLogsDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolLogs,
		Description: "Show recent application logs from the running tclaw instance. Logs are from an in-memory ring buffer (5000 lines) — only the current boot is available. For older logs, use fly logs from the CLI. Logs are filtered to your user only — other users' logs are not visible. Useful for debugging agent behavior, MCP tool calls, auth flows, scheduling, and channel issues.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"since": {
					"type": "string",
					"description": "Return only logs from this far back. Accepts '4d', '2h', '30m', '1w', or a Go duration string. Defaults to no limit (all buffered logs)."
				},
				"level": {
					"type": "string",
					"description": "Minimum log level to show. One of: DEBUG, INFO, WARN, ERROR. Defaults to DEBUG (show all).",
					"enum": ["DEBUG", "INFO", "WARN", "ERROR"]
				},
				"contains": {
					"type": "string",
					"description": "Case-insensitive substring filter. Only lines containing this text are returned."
				},
				"include_system": {
					"type": "boolean",
					"description": "Include system-wide logs (startup, shutdown, HTTP server) that aren't tagged with a specific user. Defaults to false."
				},
				"max_lines": {
					"type": "integer",
					"description": "Maximum number of log lines to return (most recent). Defaults to 100."
				}
			}
		}`),
	}
}

type devLogsArgs struct {
	Since         string `json:"since"`
	Level         string `json:"level"`
	Contains      string `json:"contains"`
	IncludeSystem bool   `json:"include_system"`
	MaxLines      int    `json:"max_lines"`
}

func devLogsHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		if deps.LogBuffer == nil {
			return nil, fmt.Errorf("log buffer not available")
		}

		var a devLogsArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		maxLines := a.MaxLines
		if maxLines <= 0 {
			maxLines = 100
		}

		var since time.Time
		if a.Since != "" {
			d, err := parseDuration(a.Since)
			if err != nil {
				return nil, fmt.Errorf("invalid since %q: %w", a.Since, err)
			}
			since = time.Now().Add(-d)
		}

		lines := deps.LogBuffer.Query(logbuffer.QueryParams{
			UserID:        string(deps.UserID),
			IncludeSystem: a.IncludeSystem,
			Level:         a.Level,
			Contains:      a.Contains,
			Since:         since,
			MaxLines:      maxLines,
		})

		result := map[string]any{
			"line_count": len(lines),
			"logs":       strings.Join(lines, "\n"),
		}
		if len(lines) == 0 {
			result["message"] = "No matching log lines found."
		}

		return json.Marshal(result)
	}
}

// parseDuration parses a human-friendly duration string. Supports shorthand
// units not covered by time.ParseDuration: d (days) and w (weeks). Falls back
// to time.ParseDuration for standard Go formats like "24h" or "90m".
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}

	// Try standard Go duration first (handles "24h", "90m", "30s", etc.).
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}

	// Parse shorthand: optional integer prefix + single-char unit.
	// Supported: Nd (days), Nw (weeks). N defaults to 1 if omitted.
	unit := s[len(s)-1:]
	numStr := s[:len(s)-1]
	if numStr == "" {
		return 0, fmt.Errorf("missing number before unit %q", unit)
	}
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("expected a positive integer before unit, got %q", numStr)
	}

	switch unit {
	case "d":
		return time.Duration(n) * 24 * time.Hour, nil
	case "w":
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown unit %q (use d, w, or a Go duration like 24h)", unit)
	}
}
