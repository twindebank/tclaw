package gmail

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/mcp"
)

type searchArgs struct {
	Connection string `json:"connection"`
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

func searchHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a searchArgs
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
				return nil, fmt.Errorf("gmail get message %s: %w", msg.Id, err)
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
