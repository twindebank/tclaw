package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/provider"

	"golang.org/x/oauth2"
	gmail "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// GmailToolsDeps holds dependencies for gmail tool handlers.
type GmailToolsDeps struct {
	ConnID   connection.ConnectionID
	Manager  *connection.Manager
	Provider *provider.Provider
}

// GmailToolDefs returns the MCP tool definitions for a gmail connection.
// Used by the provider's Tools func to advertise available tools.
func GmailToolDefs(connID connection.ConnectionID) []mcp.ToolDef {
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

// RegisterGmailTools adds all gmail tools for a specific connection to the MCP handler.
func RegisterGmailTools(h *mcp.Handler, deps GmailToolsDeps) {
	defs := GmailToolDefs(deps.ConnID)
	// defs order matches: search, read, send, reply, list_labels
	h.Register(defs[0], gmailSearchHandler(deps))
	h.Register(defs[1], gmailReadHandler(deps))
	h.Register(defs[2], gmailSendHandler(deps))
	h.Register(defs[3], gmailReplyHandler(deps))
	h.Register(defs[4], gmailListLabelsHandler(deps))
}

// gmailClient builds an authenticated Gmail API client for the connection,
// refreshing the token if needed.
func gmailClient(ctx context.Context, deps GmailToolsDeps) (*gmail.Service, error) {
	refreshFn := func(ctx context.Context, refreshToken string) (*connection.Credentials, error) {
		return oauth.RefreshToken(ctx, deps.Provider.OAuth2, refreshToken)
	}
	creds, err := deps.Manager.RefreshIfNeeded(ctx, deps.ConnID, refreshFn)
	if err != nil {
		return nil, fmt.Errorf("get credentials for %s: %w", deps.ConnID, err)
	}

	tokenSrc := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: creds.AccessToken,
	})
	svc, err := gmail.NewService(ctx, option.WithHTTPClient(oauth2.NewClient(ctx, tokenSrc)))
	if err != nil {
		return nil, fmt.Errorf("create gmail client: %w", err)
	}
	return svc, nil
}

// --- gmail_search ---

type gmailSearchArgs struct {
	Connection string `json:"connection"`
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

func gmailSearchHandler(deps GmailToolsDeps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a gmailSearchArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		maxResults := a.MaxResults
		if maxResults <= 0 {
			maxResults = 10
		}
		if maxResults > 50 {
			maxResults = 50
		}

		svc, err := gmailClient(ctx, deps)
		if err != nil {
			return nil, err
		}

		listCall := svc.Users.Messages.List("me").Q(a.Query).MaxResults(int64(maxResults))
		listRsp, err := listCall.Do()
		if err != nil {
			return nil, fmt.Errorf("gmail search: %w", err)
		}

		if len(listRsp.Messages) == 0 {
			return json.Marshal("No messages found.")
		}

		type emailSummary struct {
			ID      string `json:"id"`
			From    string `json:"from"`
			Subject string `json:"subject"`
			Date    string `json:"date"`
			Snippet string `json:"snippet"`
		}

		var results []emailSummary
		for _, msg := range listRsp.Messages {
			full, err := svc.Users.Messages.Get("me", msg.Id).Format("metadata").MetadataHeaders("From", "Subject", "Date").Do()
			if err != nil {
				continue
			}

			summary := emailSummary{
				ID:      msg.Id,
				Snippet: full.Snippet,
			}
			for _, hdr := range full.Payload.Headers {
				switch hdr.Name {
				case "From":
					summary.From = hdr.Value
				case "Subject":
					summary.Subject = hdr.Value
				case "Date":
					summary.Date = hdr.Value
				}
			}
			results = append(results, summary)
		}

		return json.Marshal(results)
	}
}

// --- gmail_read ---

type gmailReadArgs struct {
	Connection string `json:"connection"`
	MessageID  string `json:"message_id"`
}

func gmailReadHandler(deps GmailToolsDeps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a gmailReadArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		svc, err := gmailClient(ctx, deps)
		if err != nil {
			return nil, err
		}

		msg, err := svc.Users.Messages.Get("me", a.MessageID).Format("full").Do()
		if err != nil {
			return nil, fmt.Errorf("gmail read: %w", err)
		}

		type emailFull struct {
			ID       string   `json:"id"`
			ThreadID string   `json:"thread_id"`
			From     string   `json:"from"`
			To       string   `json:"to"`
			CC       string   `json:"cc,omitempty"`
			Subject  string   `json:"subject"`
			Date     string   `json:"date"`
			Labels   []string `json:"labels"`
			Body     string   `json:"body"`
		}

		result := emailFull{
			ID:       msg.Id,
			ThreadID: msg.ThreadId,
			Labels:   msg.LabelIds,
		}

		for _, hdr := range msg.Payload.Headers {
			switch hdr.Name {
			case "From":
				result.From = hdr.Value
			case "To":
				result.To = hdr.Value
			case "Cc":
				result.CC = hdr.Value
			case "Subject":
				result.Subject = hdr.Value
			case "Date":
				result.Date = hdr.Value
			}
		}

		result.Body = extractBody(msg.Payload)

		return json.Marshal(result)
	}
}

// extractBody walks the MIME parts to find the plain text body.
// Falls back to HTML if no plain text part exists.
func extractBody(payload *gmail.MessagePart) string {
	if payload == nil {
		return ""
	}

	// Single-part message.
	if payload.MimeType == "text/plain" && payload.Body != nil && payload.Body.Data != "" {
		return decodeBase64URL(payload.Body.Data)
	}

	// Multipart — look for text/plain first, then text/html.
	var htmlBody string
	for _, part := range payload.Parts {
		switch part.MimeType {
		case "text/plain":
			if part.Body != nil && part.Body.Data != "" {
				return decodeBase64URL(part.Body.Data)
			}
		case "text/html":
			if part.Body != nil && part.Body.Data != "" {
				htmlBody = decodeBase64URL(part.Body.Data)
			}
		case "multipart/alternative", "multipart/mixed", "multipart/related":
			// Recurse into nested multipart.
			if body := extractBody(part); body != "" {
				return body
			}
		}
	}

	return htmlBody
}

func decodeBase64URL(data string) string {
	decoded, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		return data
	}
	return string(decoded)
}

// --- gmail_send ---

type gmailSendArgs struct {
	Connection string `json:"connection"`
	To         string `json:"to"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	CC         string `json:"cc"`
	BCC        string `json:"bcc"`
}

func gmailSendHandler(deps GmailToolsDeps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a gmailSendArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		svc, err := gmailClient(ctx, deps)
		if err != nil {
			return nil, err
		}

		raw := buildRawEmail(a.To, a.Subject, a.Body, a.CC, a.BCC, "", "")
		msg := &gmail.Message{
			Raw: base64.URLEncoding.EncodeToString([]byte(raw)),
		}

		sent, err := svc.Users.Messages.Send("me", msg).Do()
		if err != nil {
			return nil, fmt.Errorf("gmail send: %w", err)
		}

		result := map[string]string{
			"status":     "sent",
			"message_id": sent.Id,
			"thread_id":  sent.ThreadId,
		}
		return json.Marshal(result)
	}
}

// --- gmail_reply ---

type gmailReplyArgs struct {
	Connection string `json:"connection"`
	MessageID  string `json:"message_id"`
	Body       string `json:"body"`
}

func gmailReplyHandler(deps GmailToolsDeps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a gmailReplyArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		svc, err := gmailClient(ctx, deps)
		if err != nil {
			return nil, err
		}

		// Fetch the original message to get headers for the reply.
		orig, err := svc.Users.Messages.Get("me", a.MessageID).Format("metadata").
			MetadataHeaders("From", "To", "Cc", "Subject", "Message-ID").Do()
		if err != nil {
			return nil, fmt.Errorf("gmail read original: %w", err)
		}

		var origFrom, origTo, origCC, origSubject, origMessageID string
		for _, hdr := range orig.Payload.Headers {
			switch hdr.Name {
			case "From":
				origFrom = hdr.Value
			case "To":
				origTo = hdr.Value
			case "Cc":
				origCC = hdr.Value
			case "Subject":
				origSubject = hdr.Value
			case "Message-ID":
				origMessageID = hdr.Value
			}
		}

		// Reply goes to the original sender + all original recipients.
		replyTo := origFrom
		if origTo != "" {
			replyTo += ", " + origTo
		}

		subject := origSubject
		if !strings.HasPrefix(strings.ToLower(subject), "re:") {
			subject = "Re: " + subject
		}

		raw := buildRawEmail(replyTo, subject, a.Body, origCC, "", orig.ThreadId, origMessageID)
		msg := &gmail.Message{
			Raw:      base64.URLEncoding.EncodeToString([]byte(raw)),
			ThreadId: orig.ThreadId,
		}

		sent, err := svc.Users.Messages.Send("me", msg).Do()
		if err != nil {
			return nil, fmt.Errorf("gmail reply: %w", err)
		}

		result := map[string]string{
			"status":     "sent",
			"message_id": sent.Id,
			"thread_id":  sent.ThreadId,
		}
		return json.Marshal(result)
	}
}

// --- gmail_list_labels ---

func gmailListLabelsHandler(deps GmailToolsDeps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		svc, err := gmailClient(ctx, deps)
		if err != nil {
			return nil, err
		}

		rsp, err := svc.Users.Labels.List("me").Do()
		if err != nil {
			return nil, fmt.Errorf("gmail list labels: %w", err)
		}

		type labelInfo struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		}

		var labels []labelInfo
		for _, l := range rsp.Labels {
			labels = append(labels, labelInfo{
				ID:   l.Id,
				Name: l.Name,
				Type: l.Type,
			})
		}

		return json.Marshal(labels)
	}
}

// buildRawEmail constructs an RFC 2822 email message.
func buildRawEmail(to, subject, body, cc, bcc, threadID, inReplyTo string) string {
	var b strings.Builder
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString(fmt.Sprintf("To: %s\r\n", to))
	if cc != "" {
		b.WriteString(fmt.Sprintf("Cc: %s\r\n", cc))
	}
	if bcc != "" {
		b.WriteString(fmt.Sprintf("Bcc: %s\r\n", bcc))
	}
	b.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	if inReplyTo != "" {
		b.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", inReplyTo))
		b.WriteString(fmt.Sprintf("References: %s\r\n", inReplyTo))
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}

