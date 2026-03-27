package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"tclaw/connection"
	"tclaw/gws"
	"tclaw/libraries/htmlconv"
	"tclaw/mcp"
)

type gmailReadArgs struct {
	Connection string `json:"connection"`
	MessageID  string `json:"message_id"`
}

// gmailFullMessage is the full message response from the Gmail API.
type gmailFullMessage struct {
	ID       string         `json:"id"`
	ThreadID string         `json:"threadId"`
	Snippet  string         `json:"snippet"`
	Payload  *gmailFullPart `json:"payload"`
	LabelIDs []string       `json:"labelIds"`
}

type gmailFullPart struct {
	MimeType string          `json:"mimeType"`
	Headers  []gmailHeader   `json:"headers"`
	Body     *gmailBody      `json:"body"`
	Parts    []gmailFullPart `json:"parts"`
}

type gmailBody struct {
	Size int    `json:"size"`
	Data string `json:"data"`
}

type gmailReadResponse struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
	Body    string `json:"body"`

	// ThreadID is Gmail's internal thread identifier for reply threading.
	ThreadID string `json:"thread_id"`

	// MessageID is the RFC 2822 Message-ID header — pass as in_reply_to when replying.
	MessageID string `json:"message_id_header"`

	// References is the RFC 2822 References header chain — pass to google_gmail_send for threading.
	References string `json:"references"`
}

// gmailReadHandler returns an MCP handler that fetches a single email's full body
// as clean plain text. It fetches format=full, extracts text/plain if available,
// otherwise strips HTML from text/html. This avoids dumping raw HTML blobs into
// the agent context.
func gmailReadHandler(connMap map[connection.ConnectionID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var a gmailReadArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		deps, err := resolveDeps(connMap, a.Connection)
		if err != nil {
			return nil, err
		}

		if a.MessageID == "" {
			return nil, fmt.Errorf("message_id is required")
		}

		slog.Info("gmail read starting", "connection", a.Connection, "message_id", a.MessageID)

		output, err := runGWS(ctx, deps, gws.Gmail.GetMessage(map[string]any{
			"userId": "me",
			"id":     a.MessageID,
			"format": "full",
		}))
		if err != nil {
			return nil, fmt.Errorf("get message: %w", err)
		}

		var msg gmailFullMessage
		if err := json.Unmarshal(output, &msg); err != nil {
			return nil, fmt.Errorf("parse message: %w", err)
		}

		rsp := gmailReadResponse{
			ID:       msg.ID,
			ThreadID: msg.ThreadID,
		}

		if msg.Payload != nil {
			for _, h := range msg.Payload.Headers {
				switch h.Name {
				case "From":
					rsp.From = h.Value
				case "To":
					rsp.To = h.Value
				case "Subject":
					rsp.Subject = h.Value
				case "Date":
					rsp.Date = h.Value
				case "Message-ID", "Message-Id":
					rsp.MessageID = h.Value
				case "References":
					rsp.References = h.Value
				}
			}
		}

		rsp.Body = extractBody(msg.Payload)

		slog.Info("gmail read done", "connection", a.Connection, "message_id", a.MessageID, "body_len", len(rsp.Body))

		return json.Marshal(rsp)
	}
}

// extractBody walks the MIME tree to find the best plain-text representation.
// Prefers text/plain, falls back to HTML stripped to text.
func extractBody(part *gmailFullPart) string {
	if part == nil {
		return ""
	}

	// Leaf node with a body.
	if len(part.Parts) == 0 && part.Body != nil && part.Body.Data != "" {
		decoded, err := base64.URLEncoding.DecodeString(part.Body.Data)
		if err != nil {
			// Gmail uses URL-safe base64 which may lack padding.
			decoded, err = base64.RawURLEncoding.DecodeString(part.Body.Data)
			if err != nil {
				return "(failed to decode body)"
			}
		}

		text := string(decoded)

		if strings.HasPrefix(part.MimeType, "text/plain") {
			return text
		}
		if strings.HasPrefix(part.MimeType, "text/html") {
			return htmlconv.ToText(text)
		}

		// Skip non-text parts (images, attachments).
		return ""
	}

	// Multipart — walk children, preferring text/plain over text/html.
	var plainText, htmlText string
	for i := range part.Parts {
		child := &part.Parts[i]

		if child.MimeType == "multipart/alternative" || child.MimeType == "multipart/mixed" || child.MimeType == "multipart/related" {
			// Recurse into multipart containers.
			result := extractBody(child)
			if result != "" {
				return result
			}
			continue
		}

		if strings.HasPrefix(child.MimeType, "text/plain") && child.Body != nil && child.Body.Data != "" {
			decoded := decodeBase64(child.Body.Data)
			if decoded != "" {
				plainText = decoded
			}
		}
		if strings.HasPrefix(child.MimeType, "text/html") && child.Body != nil && child.Body.Data != "" {
			decoded := decodeBase64(child.Body.Data)
			if decoded != "" {
				htmlText = decoded
			}
		}
	}

	if plainText != "" {
		return plainText
	}
	if htmlText != "" {
		return htmlconv.ToText(htmlText)
	}

	// Last resort — recurse into any remaining multipart children.
	for i := range part.Parts {
		result := extractBody(&part.Parts[i])
		if result != "" {
			return result
		}
	}

	return ""
}

func decodeBase64(data string) string {
	decoded, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(data)
		if err != nil {
			return ""
		}
	}
	return string(decoded)
}
