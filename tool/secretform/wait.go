package secretform

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"tclaw/mcp"
)

func secretFormWaitDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "secret_form_wait",
		Description: "Wait for a pending secret form to be submitted. Call this after sending the form URL to the user. Blocks until the user submits the form (up to 10 minutes).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"request_id": {
					"type": "string",
					"description": "The request ID returned by secret_form_request"
				}
			},
			"required": ["request_id"]
		}`),
	}
}

type secretFormWaitArgs struct {
	RequestID string `json:"request_id"`
}

func secretFormWaitHandler(pending *sync.Map) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a secretFormWaitArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.RequestID == "" {
			return nil, fmt.Errorf("request_id is required")
		}

		entry, ok := pending.Load(a.RequestID)
		if !ok {
			return nil, fmt.Errorf("unknown request ID %q — it may have expired or already been submitted", a.RequestID)
		}
		req := entry.(*PendingRequest)

		// Already submitted (Done channel closed).
		select {
		case <-req.Done:
			return marshalSubmittedResult(req)
		default:
		}

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		deadline := time.After(requestTTL)

		for {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("wait cancelled")

			case <-deadline:
				result := map[string]any{
					"status":  "timeout",
					"message": fmt.Sprintf("Form was not submitted within %s. The user may need a new link — call secret_form_request again.", requestTTL),
				}
				return json.Marshal(result)

			case <-req.Done:
				return marshalSubmittedResult(req)

			case <-ticker.C:
				// Keep polling.
			}
		}
	}
}

func marshalSubmittedResult(req *PendingRequest) (json.RawMessage, error) {
	keys := make([]string, len(req.Fields))
	for i, f := range req.Fields {
		keys[i] = f.Key
	}
	result := map[string]any{
		"status":  "submitted",
		"keys":    keys,
		"message": "The user submitted the form. The values are now stored securely under the listed keys.",
	}
	return json.Marshal(result)
}
