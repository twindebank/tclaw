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
			Description: "Search and list Gmail messages with full metadata (subject, from, to, cc, date, snippet, labels, is_unread) in a single call. " +
				"Use this instead of google_workspace when scanning or searching email — " +
				"Gmail's list API only returns message IDs, so this tool automatically fetches metadata for each result. " +
				"Without a query, returns the most recent messages. " +
				"Defaults: max_results=10 (max 25), query=empty (all mail). " +
				"Returns fetched_count, total_estimate, and next_page_token for pagination awareness. " +
				"IMPORTANT: Messages sharing the same thread_id are part of one email conversation — they are NOT duplicates. " +
				"The snippet field is a short preview only — do NOT extrapolate or assume email content beyond what the snippet says. " +
				"Use google_gmail_read to get the actual full body text before summarizing any email's content.",
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
			Name: "google_gmail_read",
			Description: "Read a single Gmail message and return its body as clean plain text. " +
				"Strips HTML formatting, signatures, and styling — returns only readable text content with headers (from, to, subject, date). " +
				"Use this after google_gmail_list to read specific emails you need the full content of. " +
				"You MUST read an email before summarizing its content — never guess or fabricate content from the snippet alone. " +
				"Much more efficient than google_workspace with format=full, which returns raw HTML that bloats context.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"connection": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"message_id": {
						"type": "string",
						"description": "The Gmail message ID from google_gmail_list results."
					}
				},
				"required": ["connection", "message_id"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: "google_calendar_list",
			Description: "List upcoming calendar events with full details (title, time, attendees, location, meeting links). " +
				"Returns a clean summary for each event — no need to parse raw API responses. " +
				"Defaults: days_ahead=7 (max 90), max_results=50 (max 250), calendar_id=primary. " +
				"Recurring events are expanded into individual instances. Cancelled events are excluded. " +
				"Use the query parameter to search by text (title, description, location). " +
				"For creating events, use google_calendar_create which handles formatting and duplicate detection automatically.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"connection": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"days_ahead": {
						"type": "integer",
						"description": "Number of days ahead to fetch events for. Defaults to 7, maximum 90. Starts from the beginning of today."
					},
					"query": {
						"type": "string",
						"description": "Free-text search query to filter events by title, description, or location."
					},
					"max_results": {
						"type": "integer",
						"description": "Maximum events to return. Defaults to 50, maximum 250."
					},
					"calendar_id": {
						"type": "string",
						"description": "Calendar ID. Defaults to 'primary' (the user's main calendar). Use a specific calendar ID for shared or secondary calendars."
					}
				},
				"required": ["connection"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: "google_calendar_create",
			Description: "Create a calendar event with automatic duplicate detection. " +
				"Checks for existing events with the same title on the same date before creating — " +
				"if a duplicate is found, returns the existing event instead of creating a new one. " +
				"For all-day events, provide only date. For timed events, provide date + start_time + end_time. " +
				"Times use 24h format (HH:MM). The local timezone offset is applied automatically — do NOT include timezone info in times. " +
				"For complex operations (updating events, managing attendees, recurring rules), use google_workspace directly.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"connection": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"title": {
						"type": "string",
						"description": "Event title/summary."
					},
					"date": {
						"type": "string",
						"description": "Event date in YYYY-MM-DD format."
					},
					"start_time": {
						"type": "string",
						"description": "Start time in HH:MM 24-hour format. Omit for all-day events."
					},
					"end_time": {
						"type": "string",
						"description": "End time in HH:MM 24-hour format. Omit for all-day events."
					},
					"description": {
						"type": "string",
						"description": "Event description/notes."
					},
					"location": {
						"type": "string",
						"description": "Event location (address, room name, etc.)."
					},
					"calendar_id": {
						"type": "string",
						"description": "Calendar ID. Defaults to 'primary'."
					}
				},
				"required": ["connection", "title", "date"]
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
