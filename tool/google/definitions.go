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
			Name: "google_gmail_list",
			Description: "Search and list Gmail messages with full metadata (subject, from, to, date, snippet, labels) in a single call. " +
				"Use this instead of google_workspace when scanning or searching email — " +
				"Gmail's list API only returns message IDs, so this tool automatically fetches metadata for each result. " +
				"Without a query, returns the most recent messages. " +
				"Defaults: max_results=10 (max 25), query=empty (all mail). " +
				"Returns fetched_count, total_estimate, and next_page_token for pagination awareness. " +
				"For reading a single email's full body, use google_workspace with 'gmail users messages get' instead.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"connection": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"query": {
						"type": "string",
						"description": "Gmail search query using the same syntax as the Gmail search box. Omit to list recent messages without filtering. Examples: 'from:alice@example.com', 'is:unread subject:invoice', 'after:2026/03/01 has:attachment', 'in:inbox -category:promotions'."
					},
					"max_results": {
						"type": "integer",
						"description": "Number of messages to return. Defaults to 10, maximum 25. Each message requires a separate API call internally, so keep this low for faster responses."
					}
				},
				"required": ["connection"]
			}`, connDescription, enumJSON)),
		},
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
