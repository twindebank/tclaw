package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/libraries/logbuffer"
	"tclaw/mcp"
)

func devLogsDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "dev_logs",
		Description: "Show recent application logs from the running tclaw instance. Logs are filtered to your user only — other users' logs are not visible. Useful for debugging agent behavior, MCP tool calls, auth flows, scheduling, and channel issues.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
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

		lines := deps.LogBuffer.Query(logbuffer.QueryParams{
			UserID:        string(deps.UserID),
			IncludeSystem: a.IncludeSystem,
			Level:         a.Level,
			Contains:      a.Contains,
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
