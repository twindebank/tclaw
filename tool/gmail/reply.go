package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/mcp"

	gmailapi "google.golang.org/api/gmail/v1"
)

type replyArgs struct {
	Connection string `json:"connection"`
	MessageID  string `json:"message_id"`
	Body       string `json:"body"`
}

func replyHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a replyArgs
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
		msg := &gmailapi.Message{
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
