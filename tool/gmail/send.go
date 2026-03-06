package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"tclaw/mcp"

	gmailapi "google.golang.org/api/gmail/v1"
)

type sendArgs struct {
	Connection string `json:"connection"`
	To         string `json:"to"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	CC         string `json:"cc"`
	BCC        string `json:"bcc"`
}

func sendHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a sendArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		svc, err := gmailClient(ctx, deps)
		if err != nil {
			return nil, err
		}

		raw := buildRawEmail(a.To, a.Subject, a.Body, a.CC, a.BCC, "", "")
		msg := &gmailapi.Message{
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
