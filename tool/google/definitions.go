package google

import (
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/connection"
	"tclaw/mcp"
)

// ToolDefs returns the MCP tool definitions for Google Workspace.
// connIDs lists all active connections — used to build the connection enum.
func ToolDefs(connIDs []connection.ConnectionID) []mcp.ToolDef {
	connEnum := make([]string, len(connIDs))
	for i, id := range connIDs {
		connEnum[i] = fmt.Sprintf("%q", id)
	}
	enumJSON := "[" + strings.Join(connEnum, ", ") + "]"
	connDescription := fmt.Sprintf("Connection ID to use. Available: %s", strings.Join(connEnum, ", "))

	return []mcp.ToolDef{
		{
			Name: "google_workspace",
			Description: "Execute a Google Workspace command. " +
				"Supports Gmail, Drive, Calendar, Docs, Sheets, Slides, Tasks, and more. " +
				"The 'command' is the gws CLI arguments (e.g. 'gmail users messages list', 'drive files list'). " +
				"Use google_workspace_schema to discover available methods and their parameters.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"connection": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"command": {
						"type": "string",
						"description": "The gws command arguments, e.g. 'gmail users messages list', 'drive files list', 'calendar events list', 'docs documents get'."
					},
					"params": {
						"type": "string",
						"description": "URL/query parameters as a JSON string, e.g. '{\"userId\": \"me\", \"maxResults\": 10}'. Passed as --params to gws."
					},
					"body": {
						"type": "string",
						"description": "Request body as a JSON string for POST/PATCH/PUT operations. Passed as --json to gws."
					}
				},
				"required": ["connection", "command"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: "google_workspace_schema",
			Description: "Look up the schema for a Google Workspace API method. " +
				"Returns parameter details, request/response schemas, and descriptions. " +
				"Use dotted notation like 'gmail.users.messages.list' or 'drive.files.list'.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"connection": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"method": {
						"type": "string",
						"description": "The API method in dotted notation, e.g. 'gmail.users.messages.list', 'drive.files.list', 'calendar.events.list'."
					}
				},
				"required": ["connection", "method"]
			}`, connDescription, enumJSON)),
		},
	}
}
