package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"tclaw/internal/credential"
	"tclaw/internal/gws"
	"tclaw/internal/mcp"
)

type gmailForwardArgs struct {
	CredentialSet string `json:"credential_set"`
	MessageID     string `json:"message_id"`
	To            string `json:"to"`
	CC            string `json:"cc"`
	BCC           string `json:"bcc"`
	// Note is an optional plain-text note placed above the forwarded block.
	Note string `json:"note"`
}

// gmailForwardHandler fetches the full original message and forwards it to new
// recipients. Unlike gws gmail +forward, it uses tclaw's own body extraction
// (extractBody) which converts HTML-only emails to plain text rather than
// falling back to the truncated Gmail API snippet.
func gmailForwardHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var a gmailForwardArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		deps, err := resolveDeps(depsMap, a.CredentialSet)
		if err != nil {
			return nil, err
		}

		if a.MessageID == "" {
			return nil, fmt.Errorf("message_id is required")
		}
		if a.To == "" {
			return nil, fmt.Errorf("to is required")
		}

		slog.Info("gmail forward starting", "connection", a.CredentialSet, "message_id", a.MessageID, "to", a.To)

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

		var from, to, subject, date, messageID, references, cc string
		if msg.Payload != nil {
			for _, h := range msg.Payload.Headers {
				switch h.Name {
				case "From":
					from = h.Value
				case "To":
					to = h.Value
				case "Cc", "CC":
					cc = h.Value
				case "Subject":
					subject = h.Value
				case "Date":
					date = h.Value
				case "Message-ID", "Message-Id":
					messageID = h.Value
				case "References":
					references = h.Value
				}
			}
		}

		// extractBody handles HTML-only emails by converting them to plain text,
		// avoiding the truncated snippet fallback in gws gmail +forward.
		originalBody := extractBody(msg.Payload)
		fwdBlock := buildForwardedBlock(from, to, cc, date, subject, originalBody)
		emailBody := fwdBlock
		if a.Note != "" {
			emailBody = a.Note + "\r\n\r\n" + fwdBlock
		}

		rfc2822 := buildRFC2822Message(gmailSendArgs{
			To:         a.To,
			CC:         a.CC,
			BCC:        a.BCC,
			Subject:    buildForwardSubject(subject),
			Body:       emailBody,
			InReplyTo:  messageID,
			References: buildForwardReferences(references, messageID),
		})
		encoded := base64.URLEncoding.EncodeToString([]byte(rfc2822))

		sendBody := map[string]any{"raw": encoded}
		if msg.ThreadID != "" {
			sendBody["threadId"] = msg.ThreadID
		}
		sendOutput, err := runGWS(ctx, deps, gws.Gmail.SendMessage(
			map[string]any{"userId": "me"},
			sendBody,
		))
		if err != nil {
			return nil, fmt.Errorf("send message: %w", err)
		}

		var apiResp struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		}
		if err := json.Unmarshal(sendOutput, &apiResp); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}

		slog.Info("gmail forward done", "connection", a.CredentialSet, "id", apiResp.ID, "thread_id", apiResp.ThreadID)

		rsp := struct {
			ID       string `json:"id"`
			ThreadID string `json:"thread_id"`
			Status   string `json:"status"`
		}{
			ID:       apiResp.ID,
			ThreadID: apiResp.ThreadID,
			Status:   "forwarded",
		}
		return json.Marshal(rsp)
	}
}

// buildForwardSubject prepends "Fwd: " if the subject doesn't already have it.
func buildForwardSubject(subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), "fwd:") {
		return subject
	}
	return "Fwd: " + subject
}

// buildForwardedBlock constructs the standard forwarded message attribution block.
func buildForwardedBlock(from, to, cc, date, subject, body string) string {
	var b strings.Builder
	b.WriteString("---------- Forwarded message ---------\r\n")
	if from != "" {
		b.WriteString("From: " + from + "\r\n")
	}
	if date != "" {
		b.WriteString("Date: " + date + "\r\n")
	}
	if subject != "" {
		b.WriteString("Subject: " + subject + "\r\n")
	}
	if to != "" {
		b.WriteString("To: " + to + "\r\n")
	}
	if cc != "" {
		b.WriteString("Cc: " + cc + "\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}

// buildForwardReferences appends the original message ID to the existing references chain.
func buildForwardReferences(existingRefs, messageID string) string {
	if messageID == "" {
		return existingRefs
	}
	if existingRefs == "" {
		return messageID
	}
	return existingRefs + " " + messageID
}
