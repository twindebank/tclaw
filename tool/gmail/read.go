package gmail

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/mcp"
)

type readArgs struct {
	Connection string `json:"connection"`
	MessageID  string `json:"message_id"`
}

func readHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a readArgs
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
