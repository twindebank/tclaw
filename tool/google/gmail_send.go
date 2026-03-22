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
	"tclaw/mcp"
)

type gmailSendArgs struct {
	Connection string `json:"connection"`
	To         string `json:"to"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	CC         string `json:"cc"`
	BCC        string `json:"bcc"`
	InReplyTo  string `json:"in_reply_to"`
	References string `json:"references"`
	ThreadID   string `json:"thread_id"`
}

// gmailSendHandler returns an MCP handler that sends an email via the Gmail API.
// It constructs an RFC 2822 message, base64url-encodes it, and delegates to the
// gws binary for the actual API call — consistent with the other Google tools.
func gmailSendHandler(connMap map[connection.ConnectionID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var a gmailSendArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		deps, err := resolveDeps(connMap, a.Connection)
		if err != nil {
			return nil, err
		}

		if a.To == "" {
			return nil, fmt.Errorf("to is required")
		}
		if a.Subject == "" {
			return nil, fmt.Errorf("subject is required")
		}
		if a.Body == "" {
			return nil, fmt.Errorf("body is required")
		}

		slog.Info("gmail send starting", "connection", a.Connection, "to", a.To, "subject", a.Subject)

		rfc2822 := buildRFC2822Message(a)
		encoded := base64.URLEncoding.EncodeToString([]byte(rfc2822))

		body := map[string]any{"raw": encoded}
		if a.ThreadID != "" {
			body["threadId"] = a.ThreadID
		}
		output, err := runGWS(ctx, deps, gws.Gmail.SendMessage(
			map[string]any{"userId": "me"},
			body,
		))
		if err != nil {
			return nil, fmt.Errorf("send message: %w", err)
		}

		// Parse the gws response to extract id and threadId.
		var apiResp struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		}
		if err := json.Unmarshal(output, &apiResp); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}

		slog.Info("gmail send done", "connection", a.Connection, "id", apiResp.ID, "thread_id", apiResp.ThreadID)

		rsp := struct {
			ID       string `json:"id"`
			ThreadID string `json:"thread_id"`
			Status   string `json:"status"`
		}{
			ID:       apiResp.ID,
			ThreadID: apiResp.ThreadID,
			Status:   "sent",
		}
		return json.Marshal(rsp)
	}
}

// buildRFC2822Message constructs an RFC 2822 email message from the given args.
// Gmail sets From and Date automatically from the authenticated user.
func buildRFC2822Message(a gmailSendArgs) string {
	var b strings.Builder

	b.WriteString("To: " + a.To + "\r\n")
	b.WriteString("Subject: " + a.Subject + "\r\n")

	if a.CC != "" {
		b.WriteString("Cc: " + a.CC + "\r\n")
	}
	if a.BCC != "" {
		b.WriteString("Bcc: " + a.BCC + "\r\n")
	}
	if a.InReplyTo != "" {
		b.WriteString("In-Reply-To: " + a.InReplyTo + "\r\n")
	}
	if a.References != "" {
		b.WriteString("References: " + a.References + "\r\n")
	}

	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(a.Body)

	return b.String()
}
