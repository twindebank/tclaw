package secretform

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"tclaw/internal/mcp"
)

func secretFormWaitDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolWait,
		Description: "Wait for a pending secret form to be submitted. Call after sending the form URL and verify code to the user. Each call blocks up to ~45s to stay under the MCP tool-call timeout. Returns status 'submitted' when the user has submitted, 'still_waiting' if the wait window elapsed without a submission (call again with the same request_id to keep waiting), or 'timeout' if the form's 10-minute TTL expired.",
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

		// Form-level TTL check first. If the form has already expired server-side,
		// there's no point waiting — the user cannot submit it.
		ttlRemaining := requestTTL - time.Since(req.CreatedAt)
		if ttlRemaining <= 0 {
			return marshalTimeoutResult()
		}

		// Short per-call wait window to stay under the CLI's tool-call timeout.
		// If the form isn't submitted within it, return still_waiting so the
		// agent can call again without the CLI cancelling us mid-wait.
		waitFor := maxWaitPerCall
		if waitFor > ttlRemaining {
			waitFor = ttlRemaining
		}
		windowTimer := time.NewTimer(waitFor)
		defer windowTimer.Stop()

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait cancelled")

		case <-req.Done:
			return marshalSubmittedResult(req)

		case <-windowTimer.C:
			// Re-check submission (it may have completed just as the timer fired).
			select {
			case <-req.Done:
				return marshalSubmittedResult(req)
			default:
			}
			// If we ran for the full per-call window, the form is still waiting
			// for submission — tell the agent to loop. Otherwise the form's
			// overall TTL elapsed and we report timeout.
			if waitFor < maxWaitPerCall {
				return marshalTimeoutResult()
			}
			result := map[string]any{
				"status":     "still_waiting",
				"request_id": a.RequestID,
				"message":    "The form has not been submitted yet. Call secret_form_wait again with the same request_id to keep waiting.",
			}
			return json.Marshal(result)
		}
	}
}

func marshalTimeoutResult() (json.RawMessage, error) {
	result := map[string]any{
		"status":  "timeout",
		"message": fmt.Sprintf("Form was not submitted within %s. The user may need a new link — call secret_form_request again.", requestTTL),
	}
	return json.Marshal(result)
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
