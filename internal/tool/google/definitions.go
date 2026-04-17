package google

import (
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/internal/credential"
	"tclaw/internal/mcp"
)

const (
	ToolGmailList       = "google_gmail_list"
	ToolGmailRead       = "google_gmail_read"
	ToolGmailSend       = "google_gmail_send"
	ToolCalendarList    = "google_calendar_list"
	ToolCalendarCreate  = "google_calendar_create"
	ToolWorkspace       = "google_workspace"
	ToolWorkspaceSchema = "google_workspace_schema"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolGmailList, ToolGmailRead, ToolGmailSend,
		ToolCalendarList, ToolCalendarCreate,
		ToolWorkspace, ToolWorkspaceSchema,
	}
}

// ToolDefs returns the MCP tool definitions for Google Workspace.
// connIDs lists all active connections — used to build the connection enum.
func ToolDefs(connIDs []credential.CredentialSetID) []mcp.ToolDef {
	connEnum := make([]string, len(connIDs))
	for i, id := range connIDs {
		connEnum[i] = fmt.Sprintf("%q", id)
	}
	enumJSON := "[" + strings.Join(connEnum, ", ") + "]"
	connDescription := fmt.Sprintf("Credential set ID to use. Available: %s", strings.Join(connEnum, ", "))

	return []mcp.ToolDef{
		{
			Name: ToolGmailList,
			Description: "Search and list Gmail messages with full metadata (subject, from, to, cc, date, snippet, labels, is_unread) in a single call. " +
				"Use this instead of google_workspace when scanning or searching email — " +
				"Gmail's list API only returns message IDs, so this tool automatically fetches metadata for each result. " +
				"Without a query, returns the most recent messages. " +
				"Defaults: max_results=10 (max 25), query=empty (all mail). " +
				"Returns fetched_count, total_estimate, and next_page_token for pagination awareness. " +
				"PAGINATION: returns at most 25 results per call. If a call returns exactly 25 results, paginate using next_page_token until done — otherwise you'll silently miss messages. " +
				"Don't filter by category/label when doing a comprehensive scan — Gmail categorisation (Promotions, Updates) can hide important emails. " +
				"Results are authoritative — don't re-list to double-check. " +
				"IMPORTANT: Messages sharing the same thread_id are part of one email conversation — they are NOT duplicates. " +
				"The snippet field is a short preview only — do NOT extrapolate or assume email content beyond what the snippet says. " +
				"Use google_gmail_read to get the actual full body text before summarizing any email's content.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
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
					},
					"page_token": {
						"type": "string",
						"description": "Page token from a previous google_gmail_list response (next_page_token field). Pass this to fetch the next page of results."
					}
				},
				"required": ["credential_set"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: ToolGmailRead,
			Description: "Read a single Gmail message and return its body as clean plain text. " +
				"Converts HTML emails (marketing newsletters, booking confirmations, etc.) to readable text — " +
				"preserves table content (prices, dates, structured data), link text, and block formatting. " +
				"Use this after google_gmail_list to read specific emails you need the full content of. " +
				"You MUST read an email before summarizing its content — never guess or fabricate content from the snippet alone. " +
				"Much more efficient than google_workspace with format=full, which returns raw HTML that bloats context.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"message_id": {
						"type": "string",
						"description": "The Gmail message ID from google_gmail_list results."
					}
				},
				"required": ["credential_set","message_id"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: ToolGmailSend,
			Description: "Send an email via Gmail. Handles RFC 2822 message construction and base64url encoding internally — " +
				"no Bash or external encoding needed. " +
				"For replies: use google_gmail_read first to get the message_id (use as in_reply_to), references, and thread_id, " +
				"then pass them here to thread the reply correctly. " +
				"TIP: For simpler replies, use google_workspace with 'gmail +reply --message-id ID --body TEXT' — " +
				"it handles threading headers automatically without needing to read the message first. " +
				"Sends plain text emails. The From address is set automatically from the authenticated Google account.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"to": {
						"type": "string",
						"description": "Recipient email address(es), comma-separated for multiple."
					},
					"subject": {
						"type": "string",
						"description": "Email subject line."
					},
					"body": {
						"type": "string",
						"description": "Plain text email body."
					},
					"cc": {
						"type": "string",
						"description": "CC recipient(s), comma-separated."
					},
					"bcc": {
						"type": "string",
						"description": "BCC recipient(s), comma-separated."
					},
					"in_reply_to": {
						"type": "string",
						"description": "Message-ID header from the email being replied to (from google_gmail_read response). Required for proper reply threading."
					},
					"references": {
						"type": "string",
						"description": "References header chain from the original email (from google_gmail_read response). Helps mail clients thread the conversation."
					},
					"thread_id": {
						"type": "string",
						"description": "Gmail thread ID to place the reply in the same conversation thread (from google_gmail_read or google_gmail_list)."
					}
				},
				"required": ["credential_set","to", "subject", "body"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: ToolCalendarList,
			Description: "List calendar events with full details (title, time, attendees, location, meeting links). " +
				"Returns a clean summary for each event — no need to parse raw API responses. " +
				"Defaults: days_ahead=7 (max 90), max_results=50 (max 250), calendar_id=primary. " +
				"Recurring events are expanded into individual instances. Cancelled events are excluded. " +
				"Use the query parameter to search by text (title, description, location). " +
				"Use start_date (YYYY-MM-DD) to query a window starting on a specific date instead of today — days_ahead still controls the length. " +
				"NOTE: time_min and time_max are NOT valid parameters — they are silently ignored. Use start_date + days_ahead instead. " +
				"For creating events, use google_calendar_create which handles formatting and duplicate detection automatically.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"start_date": {
						"type": "string",
						"description": "Start date for the query window (YYYY-MM-DD). When provided, the window starts at the beginning of this date instead of today. days_ahead still controls the window length from this start date. Example: start_date='2026-06-01', days_ahead=14 fetches 1–15 Jun."
					},
					"days_ahead": {
						"type": "integer",
						"description": "Number of days ahead to fetch events for. Defaults to 7, maximum 90. Starts from start_date if provided, otherwise from today."
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
					},
					"page_token": {
						"type": "string",
						"description": "Page token from a previous google_calendar_list response (next_page_token field). Pass this to fetch the next page of results."
					}
				},
				"required": ["credential_set"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: ToolCalendarCreate,
			Description: "Create a calendar event with automatic duplicate detection. " +
				"Checks for existing events with the same title on the same date before creating — " +
				"if a duplicate is found, returns the existing event instead of creating a new one. " +
				"For single-day all-day events, provide only date. For multi-day all-day events (e.g. hotel stays, trips), provide date + end_date — both in YYYY-MM-DD format, end_date is the inclusive last day. For timed events, provide date + start_time + end_time. " +
				"Times use 24h format (HH:MM). The local timezone offset is applied automatically — do NOT include timezone info in times. " +
				"Set add_meet=true to automatically attach a Google Meet video conference link. " +
				"For complex operations (updating events, managing attendees, recurring rules), use google_workspace directly.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
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
						"description": "Event start date in YYYY-MM-DD format."
					},
					"end_date": {
						"type": "string",
						"description": "Inclusive end date for multi-day all-day events (YYYY-MM-DD). Only valid for all-day events (omit start_time/end_time). Must be after date. For single-day all-day events, omit this field."
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
					},
					"add_meet": {
						"type": "boolean",
						"description": "If true, attaches a Google Meet video conference link to the event."
					}
				},
				"required": ["credential_set","title", "date"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: ToolWorkspace,
			Description: "Execute a Google Workspace command. " +
				"Supports Gmail, Drive, Calendar, Docs, Sheets, Slides, Tasks, and more. " +
				"The 'command' is the gws CLI arguments (e.g. 'gmail users messages list', 'drive files list'). " +
				"Use google_workspace_schema to discover available methods and their parameters. " +
				"gws also has built-in skills ('+' commands) for common workflows — run 'gws gmail --help' to see them. " +
				"Key skills: 'gmail +triage' (unread summary), 'gmail +reply --message-id ID --body TEXT' (auto-threaded reply), " +
				"'gmail +forward --message-id ID --to ADDR' (forward with attachments), 'gmail +send' (compose). " +
				"Skills handle threading headers, MIME encoding, and attachments automatically. " +
				"Skill reference: https://github.com/googleworkspace/cli/tree/main/skills " +
				"IMPORTANT: Never use with Gmail format=full — it returns huge HTML blobs that waste context. Use google_gmail_read instead. " +
				"Calendar updates: use 'calendar events update' (full PUT), NOT 'calendar events patch' — patching date to dateTime causes a 400. " +
				"For timezone in dateTime, use UTC offset in the ISO string (e.g. 2026-03-13T17:26:00+00:00), NOT a separate timeZone field. " +
				"Sheets writes: all write operations (values.update, batchUpdate, values.clear, etc.) require the 'json' field — pass the request body as a JSON string there. Example: command='sheets spreadsheets values update', params='{\"spreadsheetId\":\"...\",\"range\":\"Sheet1!A1\",\"valueInputOption\":\"RAW\"}', json='{\"values\":[[\"hello\"]]}'.\n\n" +
				"READING PDF ATTACHMENTS from Gmail:\n" +
				"1. google_workspace with 'gmail users messages get', format=full — result is saved to a file (too large for context)\n" +
				"2. Use node to parse the file and find attachment IDs: iterate payload.parts recursively, look for body.attachmentId and filename\n" +
				"3. google_workspace with 'gmail users messages attachments get', params {userId:\"me\", messageId:\"...\", id:\"<attachmentId>\"} — also saved to file\n" +
				"4. Use node to parse and base64-decode: obj.data.replace(/-/g,'+').replace(/_/g,'/'), then Buffer.from(b64,'base64'), write to /tmp/filename.pdf\n" +
				"5. Use Read tool on the saved PDF — Claude can view it directly as a multimodal input",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
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
					"json": {
						"type": "string",
						"description": "Request body as a JSON string for POST/PATCH/PUT operations. Passed as --json to gws."
					}
				},
				"required": ["credential_set","command"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: ToolWorkspaceSchema,
			Description: "Look up the schema for a Google Workspace API method. " +
				"Returns parameter details, request/response schemas, and descriptions. " +
				"Use dotted notation like 'gmail.users.messages.list' or 'drive.files.list'.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"method": {
						"type": "string",
						"description": "The API method in dotted notation, e.g. 'gmail.users.messages.list', 'drive.files.list', 'calendar.events.list'."
					}
				},
				"required": ["credential_set","method"]
			}`, connDescription, enumJSON)),
		},
	}
}
