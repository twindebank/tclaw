package gmail

import (
	"encoding/json"
	"fmt"

	"tclaw/connection"
	"tclaw/mcp"
)

// ToolDefs returns the MCP tool definitions for a gmail connection.
// Used by the provider's Tools func to advertise available tools.
func ToolDefs(connID connection.ConnectionID) []mcp.ToolDef {
	connParam := fmt.Sprintf(`"connection": {"type": "string", "description": "Connection ID to use.", "const": %q}`, connID)

	return []mcp.ToolDef{
		{
			Name:        "gmail_search",
			Description: fmt.Sprintf("Search emails in %s using Gmail query syntax (e.g. 'from:alice subject:invoice is:unread'). Returns message summaries.", connID),
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					%s,
					"query": {"type": "string", "description": "Gmail search query (same syntax as the Gmail search bar)."},
					"max_results": {"type": "integer", "description": "Maximum number of results to return (default 10, max 50)."}
				},
				"required": ["connection", "query"]
			}`, connParam)),
		},
		{
			Name:        "gmail_read",
			Description: fmt.Sprintf("Read the full content of an email by message ID from %s. Use gmail_search first to find message IDs.", connID),
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					%s,
					"message_id": {"type": "string", "description": "The message ID to read (from gmail_search results)."}
				},
				"required": ["connection", "message_id"]
			}`, connParam)),
		},
		{
			Name:        "gmail_send",
			Description: fmt.Sprintf("Send a new email from %s.", connID),
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					%s,
					"to": {"type": "string", "description": "Recipient email address(es), comma-separated."},
					"subject": {"type": "string", "description": "Email subject line."},
					"body": {"type": "string", "description": "Email body (plain text)."},
					"cc": {"type": "string", "description": "CC recipients, comma-separated."},
					"bcc": {"type": "string", "description": "BCC recipients, comma-separated."}
				},
				"required": ["connection", "to", "subject", "body"]
			}`, connParam)),
		},
		{
			Name:        "gmail_reply",
			Description: fmt.Sprintf("Reply to an existing email thread in %s. The reply is sent to all recipients of the original message.", connID),
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					%s,
					"message_id": {"type": "string", "description": "The message ID to reply to."},
					"body": {"type": "string", "description": "Reply body (plain text)."}
				},
				"required": ["connection", "message_id", "body"]
			}`, connParam)),
		},
		{
			Name:        "gmail_list_labels",
			Description: fmt.Sprintf("List all labels (folders) in %s.", connID),
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					%s
				},
				"required": ["connection"]
			}`, connParam)),
		},
	}
}
