// Package google implements Google Workspace MCP tools.
//
// # When to add a dedicated tool vs. use the passthrough
//
// google_workspace is a generic passthrough to the gws CLI — it covers every
// Gmail/Drive/Calendar/Docs/Sheets/Slides/Tasks operation, but the agent must
// hand-assemble command/params/json cold on every call. Add a dedicated typed
// tool (like google_gmail_modify, google_gmail_forward) only when an action is
// used OFTEN — frequent, repeated operations justify the upkeep of a typed
// schema, a handler, and tests. One-off or rarely-used operations should stay
// on the passthrough; adding a dedicated tool for every gws call would bloat
// this package for no benefit.
//
// When you do add one:
//  1. Add a Tool* constant below, include it in ToolNames(), and add its
//     mcp.ToolDef in ToolDefs().
//  2. Implement the handler in its own file (see gmail_forward.go for the
//     template) and register it in google.go's RegisterTools — note the
//     registration is positional (defs[N] matches ToolDefs()'s slice order),
//     so inserting a tool mid-list means renumbering every later index.
//  3. Point the google_workspace tool description (below) and the
//     `gws-tclaw` skill (internal/router/gws_tclaw_skill.md) at the new
//     dedicated tool so agents stop falling through to the passthrough for it.
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
	ToolGmailForward    = "google_gmail_forward"
	ToolGmailModify     = "google_gmail_modify"
	ToolCalendarList    = "google_calendar_list"
	ToolCalendarCreate  = "google_calendar_create"
	ToolCalendarUpdate  = "google_calendar_update"
	ToolWorkspace       = "google_workspace"
	ToolWorkspaceSchema = "google_workspace_schema"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolGmailList, ToolGmailRead, ToolGmailSend, ToolGmailForward, ToolGmailModify,
		ToolCalendarList, ToolCalendarCreate, ToolCalendarUpdate,
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
			Name: ToolGmailForward,
			Description: "Forward a Gmail message to new recipients with the complete original body. " +
				"Fetches the full message and correctly handles HTML-only emails by converting them to plain text — " +
				"unlike gws gmail +forward which truncates HTML-only emails to the Gmail snippet. " +
				"Use this instead of google_workspace 'gmail +forward' whenever forwarding an email. " +
				"Threading headers (In-Reply-To, References) are set automatically. " +
				"The From address is set automatically from the authenticated Google account.",
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
						"description": "The Gmail message ID to forward (from google_gmail_list or google_gmail_read)."
					},
					"to": {
						"type": "string",
						"description": "Recipient email address(es), comma-separated for multiple."
					},
					"cc": {
						"type": "string",
						"description": "CC recipient(s), comma-separated."
					},
					"bcc": {
						"type": "string",
						"description": "BCC recipient(s), comma-separated."
					},
					"note": {
						"type": "string",
						"description": "Optional plain-text note to include above the forwarded message block."
					}
				},
				"required": ["credential_set","message_id","to"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: ToolGmailModify,
			Description: "Modify labels on a Gmail message — mark as read/unread, apply or remove labels, archive, etc. " +
				"Use this instead of google_workspace 'gmail users messages modify' whenever changing a message's label state. " +
				"To mark read: remove_label_ids=[\"UNREAD\"]. To apply a label: add_label_ids=[\"LABEL_ID\"]. " +
				"Both can be set in a single call (e.g. label + mark read together). " +
				"Get label IDs from google_workspace_schema or from a prior google_gmail_list/read call's labels field.",
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
						"description": "The Gmail message ID to modify (from google_gmail_list or google_gmail_read)."
					},
					"add_label_ids": {
						"type": "array",
						"items": {"type": "string"},
						"description": "Label IDs to add to the message, e.g. [\"Label_36\"]."
					},
					"remove_label_ids": {
						"type": "array",
						"items": {"type": "string"},
						"description": "Label IDs to remove from the message, e.g. [\"UNREAD\"] to mark as read."
					}
				},
				"required": ["credential_set","message_id"]
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
				"Checks for an existing event with the same title on the same date first — if found, returns it instead of creating a duplicate. " +
				"Event type must be explicit to avoid accidental all-day events: " +
				"for a TIMED event provide date + start_time + end_time (24h HH:MM); " +
				"for an ALL-DAY event set all_day=true (add end_date for multi-day stays like hotels/trips — inclusive last day, YYYY-MM-DD). " +
				"A bare date with no times and no all_day is REJECTED with an error. " +
				"timezone is an IANA name (e.g. 'Asia/Tokyo', 'Europe/Lisbon') for the event's local wall-clock time — defaults to Europe/London. " +
				"Always set it for events in another timezone (travel/trips) so start/end land at the correct local time; do NOT put offsets in the times. " +
				"Set add_meet=true to attach a Google Meet video conference link. " +
				"To reschedule or modify an existing event, use google_calendar_update. For attendees/recurring rules, use google_workspace directly.",
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
						"description": "Inclusive end date for multi-day all-day events (YYYY-MM-DD). Only valid for all-day events (set all_day=true, omit start_time/end_time). Must be after date. For single-day all-day events, omit this field."
					},
					"start_time": {
						"type": "string",
						"description": "Start time in HH:MM 24-hour format. Provide together with end_time for a timed event. Omit for all-day events."
					},
					"end_time": {
						"type": "string",
						"description": "End time in HH:MM 24-hour format. Provide together with start_time for a timed event. Omit for all-day events."
					},
					"all_day": {
						"type": "boolean",
						"description": "Set true to create an all-day event (no times). Required for all-day events — a bare date without times or all_day is rejected."
					},
					"timezone": {
						"type": "string",
						"description": "IANA timezone name for the event's local time (e.g. 'Asia/Tokyo', 'Europe/Lisbon', 'America/New_York'). Defaults to Europe/London. Set this for events in another timezone so the time is stored correctly. Ignored for all-day events."
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
			Name: ToolCalendarUpdate,
			Description: "Update an existing calendar event by event_id — reschedule, rename, move, or edit details. " +
				"Fetches the event and applies a full update, so fields you omit are preserved (attendees, reminders, Meet link, etc.). " +
				"Provide ONLY the fields you want to change: title, description, location, and/or timing. " +
				"Timing: to change the time provide start_time + end_time (both required); to move to another day provide date; to make it all-day provide all_day=true (with optional end_date). " +
				"When you change the time without a date, the event stays on its original day; when you omit timezone, it keeps its existing timezone. " +
				"timezone is an IANA name (e.g. 'Asia/Tokyo') — set it to move the event into another timezone (travel/trips). Do NOT put offsets in the times. " +
				"Get the event_id from google_calendar_list. To create a new event, use google_calendar_create.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"event_id": {
						"type": "string",
						"description": "ID of the event to update (from google_calendar_list results)."
					},
					"title": {
						"type": "string",
						"description": "New event title/summary. Omit to leave unchanged."
					},
					"date": {
						"type": "string",
						"description": "New start date (YYYY-MM-DD). Omit to keep the event on its current day when only changing the time."
					},
					"end_date": {
						"type": "string",
						"description": "New inclusive end date for a multi-day all-day event (YYYY-MM-DD). Must be after date."
					},
					"start_time": {
						"type": "string",
						"description": "New start time in HH:MM 24-hour format. Provide together with end_time to (re)time the event."
					},
					"end_time": {
						"type": "string",
						"description": "New end time in HH:MM 24-hour format. Provide together with start_time to (re)time the event."
					},
					"all_day": {
						"type": "boolean",
						"description": "Set true to convert the event to all-day (drops the times). Only needed when changing the event type."
					},
					"timezone": {
						"type": "string",
						"description": "IANA timezone name for a timed event (e.g. 'Asia/Tokyo'). Omit to keep the event's existing timezone. Set it to move the event into another timezone."
					},
					"description": {
						"type": "string",
						"description": "New description/notes. Omit to leave unchanged."
					},
					"location": {
						"type": "string",
						"description": "New location. Omit to leave unchanged."
					},
					"calendar_id": {
						"type": "string",
						"description": "Calendar ID. Defaults to 'primary'."
					}
				},
				"required": ["credential_set","event_id"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: ToolWorkspace,
			Description: "Generic passthrough to the Google Workspace CLI (gws) for any Gmail/Drive/Calendar/Docs/Sheets/Slides/Tasks operation not covered by a dedicated tool. " +
				"BEFORE using this, read the 'gws-tclaw' skill — it explains how to translate a gws CLI example into this tool's arguments and records tclaw-specific API gotchas (calendar, sheets, PDF attachments). The per-service 'gws-*' skills document every command. " +
				"Mapping: a skill's 'gws <args> --params <P> --json <B>' becomes command=\"<args>\", params=<P>, json=<B>. " +
				"Example: command='gmail users messages modify', params='{\"userId\":\"me\",\"id\":\"MSG_ID\"}', json='{\"addLabelIds\":[\"LABEL_ID\"],\"removeLabelIds\":[\"UNREAD\"]}'. " +
				"Use google_workspace_schema to inspect a method's parameters. " +
				"Prefer the dedicated tools where they exist: google_gmail_list (search/scan), google_gmail_read (read a body — NEVER use Gmail format=full here, it floods context with raw HTML), google_gmail_forward (forward), google_gmail_modify (label/mark-read/mark-unread). " +
				"Downloads (e.g. 'drive files get' with alt=media) are written to a file rather than returned inline — the response's saved_file field is the absolute path to read the downloaded bytes from.",
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
