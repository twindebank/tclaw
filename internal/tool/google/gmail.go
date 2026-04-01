package google

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"tclaw/internal/credential"
	"tclaw/internal/gws"
	"tclaw/internal/mcp"
)

const (
	// Gmail's list endpoint only returns message IDs — no subject, sender, or snippet.
	// This wrapper does list + concurrent metadata gets so the agent gets a scannable
	// summary in a single tool call. Capped at 25 to stay well within Gmail API quota
	// (250 quota units/second; each messages.get costs 5 units).
	maxGmailListResults = 25

	// gmailMetadataConcurrency limits parallel messages.get calls to avoid bursting
	// the per-user Gmail API rate limit.
	gmailMetadataConcurrency = 5
)

type gmailListArgs struct {
	CredentialSet string `json:"credential_set"`
	Query         string `json:"query"`
	MaxResults    int    `json:"max_results"`
	PageToken     string `json:"page_token"`
}

// gmailListResponse matches the Gmail API's users.messages.list response.
type gmailListResponse struct {
	Messages           []gmailMessageRef `json:"messages"`
	NextPageToken      string            `json:"nextPageToken,omitempty"`
	ResultSizeEstimate int               `json:"resultSizeEstimate,omitempty"`
}

type gmailMessageRef struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
}

// gmailMessageMetadata is a subset of the full message response.
type gmailMessageMetadata struct {
	ID           string        `json:"id"`
	ThreadID     string        `json:"threadId"`
	LabelIDs     []string      `json:"labelIds"`
	Snippet      string        `json:"snippet"`
	InternalDate string        `json:"internalDate"`
	Payload      *gmailPayload `json:"payload"`
}

type gmailPayload struct {
	Headers []gmailHeader `json:"headers"`
}

type gmailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// gmailSummary is the enriched per-message summary returned to the agent.
type gmailSummary struct {
	ID       string   `json:"id"`
	ThreadID string   `json:"thread_id"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	CC       string   `json:"cc,omitempty"`
	Subject  string   `json:"subject"`
	Date     string   `json:"date"`
	Snippet  string   `json:"snippet"`
	Labels   []string `json:"labels"`
	IsUnread bool     `json:"is_unread"`
}

type gmailListToolResponse struct {
	Messages        []gmailSummary `json:"messages"`
	TotalEstimate   int            `json:"total_estimate,omitempty"`
	NextPageToken   string         `json:"next_page_token,omitempty"`
	FetchedCount    int            `json:"fetched_count"`
	FetchErrorCount int            `json:"fetch_error_count,omitempty"`
}

// gmailListHandler returns an MCP handler that searches/lists Gmail messages
// with enriched metadata. It performs two steps internally:
//  1. Calls gmail users.messages.list to get matching message IDs
//  2. Concurrently fetches metadata (Subject, From, To, Date, snippet, labels)
//     for each message using format=metadata
//
// This avoids the agent needing two separate tool calls per email scan.
func gmailListHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var a gmailListArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		deps, err := resolveDeps(depsMap, a.CredentialSet)
		if err != nil {
			return nil, err
		}

		maxResults := a.MaxResults
		if maxResults <= 0 {
			maxResults = 10
		}
		if maxResults > maxGmailListResults {
			maxResults = maxGmailListResults
		}

		slog.Info("gmail list starting", "connection", a.CredentialSet, "query", a.Query, "max_results", maxResults)

		// Step 1: list message IDs.
		listParams := map[string]any{
			"userId":     "me",
			"maxResults": maxResults,
		}
		if a.Query != "" {
			listParams["q"] = a.Query
		}
		if a.PageToken != "" {
			listParams["pageToken"] = a.PageToken
		}

		listOutput, err := runGWS(ctx, deps, gws.Gmail.ListMessages(listParams))
		if err != nil {
			slog.Error("gmail list failed", "connection", a.CredentialSet, "error", err)
			return nil, fmt.Errorf("list messages: %w", err)
		}

		var listRsp gmailListResponse
		if err := json.Unmarshal(listOutput, &listRsp); err != nil {
			slog.Error("gmail list response parse failed", "connection", a.CredentialSet, "error", err, "raw_output_len", len(listOutput))
			return nil, fmt.Errorf("parse list response: %w", err)
		}

		slog.Info("gmail list results", "connection", a.CredentialSet, "message_count", len(listRsp.Messages), "total_estimate", listRsp.ResultSizeEstimate)

		if len(listRsp.Messages) == 0 {
			return json.Marshal(gmailListToolResponse{
				Messages:      []gmailSummary{},
				TotalEstimate: listRsp.ResultSizeEstimate,
			})
		}

		// Step 2: fetch metadata for each message concurrently.
		type indexedResult struct {
			index   int
			summary gmailSummary
			err     error
		}

		results := make([]indexedResult, len(listRsp.Messages))
		var wg sync.WaitGroup

		semaphore := make(chan struct{}, gmailMetadataConcurrency)

		for i, msg := range listRsp.Messages {
			wg.Add(1)
			go func(idx int, msgID string) {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				output, err := runGWS(ctx, deps, gws.Gmail.GetMessage(map[string]any{
					"userId":          "me",
					"id":              msgID,
					"format":          "metadata",
					"metadataHeaders": "Subject,From,To,Cc,Date",
				}))
				if err != nil {
					slog.Warn("gmail message metadata fetch failed", "message_id", msgID, "error", err)
					results[idx] = indexedResult{index: idx, err: err}
					return
				}

				var meta gmailMessageMetadata
				if err := json.Unmarshal(output, &meta); err != nil {
					slog.Warn("gmail message metadata parse failed", "message_id", msgID, "error", err)
					results[idx] = indexedResult{index: idx, err: err}
					return
				}

				results[idx] = indexedResult{
					index:   idx,
					summary: extractSummary(meta),
				}
			}(i, msg.ID)
		}
		wg.Wait()

		summaries := make([]gmailSummary, 0, len(results))
		errorCount := 0
		for _, r := range results {
			if r.err != nil {
				errorCount++
				continue
			}
			summaries = append(summaries, r.summary)
		}

		if errorCount > 0 {
			slog.Warn("gmail metadata fetch errors", "connection", a.CredentialSet, "fetched", len(summaries), "errors", errorCount)
		}

		return json.Marshal(gmailListToolResponse{
			Messages:        summaries,
			TotalEstimate:   listRsp.ResultSizeEstimate,
			NextPageToken:   listRsp.NextPageToken,
			FetchedCount:    len(summaries),
			FetchErrorCount: errorCount,
		})
	}
}

func extractSummary(meta gmailMessageMetadata) gmailSummary {
	s := gmailSummary{
		ID:       meta.ID,
		ThreadID: meta.ThreadID,
		Snippet:  meta.Snippet,
		Labels:   meta.LabelIDs,
	}

	for _, label := range meta.LabelIDs {
		if label == "UNREAD" {
			s.IsUnread = true
			break
		}
	}

	if meta.Payload != nil {
		for _, h := range meta.Payload.Headers {
			switch h.Name {
			case "From":
				s.From = h.Value
			case "To":
				s.To = h.Value
			case "Cc":
				s.CC = h.Value
			case "Subject":
				s.Subject = h.Value
			case "Date":
				s.Date = h.Value
			}
		}
	}

	return s
}
