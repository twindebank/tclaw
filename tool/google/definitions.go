package google

import (
	"encoding/json"
	"fmt"

	"tclaw/connection"
	"tclaw/mcp"
)

// ToolDefs returns the MCP tool definitions for a Google Workspace connection.
func ToolDefs(connID connection.ConnectionID) []mcp.ToolDef {
	connParam := fmt.Sprintf(`"connection": {"type": "string", "description": "Connection ID to use.", "const": %q}`, connID)

	return []mcp.ToolDef{
		{
			Name: "google_workspace",
			Description: fmt.Sprintf(
				"Execute a Google Workspace command via %s. "+
					"Supports Gmail, Drive, Calendar, Docs, Sheets, Slides, Tasks, and more. "+
					"The 'command' is the gws CLI arguments (e.g. 'gmail users messages list', 'drive files list'). "+
					"Use google_workspace_schema to discover available methods and their parameters.",
				connID,
			),
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					%s,
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
			}`, connParam)),
		},
		{
			Name: "google_workspace_schema",
			Description: fmt.Sprintf(
				"Look up the schema for a Google Workspace API method via %s. "+
					"Returns parameter details, request/response schemas, and descriptions. "+
					"Use dotted notation like 'gmail.users.messages.list' or 'drive.files.list'.",
				connID,
			),
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					%s,
					"method": {
						"type": "string",
						"description": "The API method in dotted notation, e.g. 'gmail.users.messages.list', 'drive.files.list', 'calendar.events.list'."
					}
				},
				"required": ["connection", "method"]
			}`, connParam)),
		},
	}
}
