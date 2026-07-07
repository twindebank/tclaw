package google

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tclaw/internal/credential"
	"tclaw/internal/gws"
	"tclaw/internal/mcp"
)

type gmailModifyArgs struct {
	CredentialSet  string   `json:"credential_set"`
	MessageID      string   `json:"message_id"`
	AddLabelIDs    []string `json:"add_label_ids"`
	RemoveLabelIDs []string `json:"remove_label_ids"`
}

// gmailModifyHandler adds and/or removes labels on a Gmail message in a single
// call — the common cases are marking read/unread (UNREAD label) and applying
// a category label, often both at once during email triage.
func gmailModifyHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var a gmailModifyArgs
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
		if len(a.AddLabelIDs) == 0 && len(a.RemoveLabelIDs) == 0 {
			return nil, fmt.Errorf("at least one of add_label_ids or remove_label_ids is required")
		}

		slog.Info("gmail modify starting", "connection", a.CredentialSet, "message_id", a.MessageID,
			"add_label_ids", a.AddLabelIDs, "remove_label_ids", a.RemoveLabelIDs)

		body := buildModifyBody(a.AddLabelIDs, a.RemoveLabelIDs)

		output, err := runGWS(ctx, deps, gws.Gmail.ModifyMessage(
			map[string]any{"userId": "me", "id": a.MessageID},
			body,
		))
		if err != nil {
			return nil, fmt.Errorf("modify message: %w", err)
		}

		var apiResp struct {
			ID       string   `json:"id"`
			ThreadID string   `json:"threadId"`
			LabelIDs []string `json:"labelIds"`
		}
		if err := json.Unmarshal(output, &apiResp); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}

		slog.Info("gmail modify done", "connection", a.CredentialSet, "id", apiResp.ID, "labels", apiResp.LabelIDs)

		rsp := struct {
			ID       string   `json:"id"`
			ThreadID string   `json:"thread_id"`
			Labels   []string `json:"labels"`
			Status   string   `json:"status"`
		}{
			ID:       apiResp.ID,
			ThreadID: apiResp.ThreadID,
			Labels:   apiResp.LabelIDs,
			Status:   "modified",
		}
		return json.Marshal(rsp)
	}
}

// buildModifyBody constructs the addLabelIds/removeLabelIds request body from
// the given label ID lists, omitting fields with no entries so an empty list
// doesn't clear labels the caller didn't intend to touch.
func buildModifyBody(addLabelIDs, removeLabelIDs []string) map[string]any {
	body := map[string]any{}
	if len(addLabelIDs) > 0 {
		body["addLabelIds"] = addLabelIDs
	}
	if len(removeLabelIDs) > 0 {
		body["removeLabelIds"] = removeLabelIDs
	}
	return body
}
