package devtools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/mcp"
)

func devCancelDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "dev_cancel",
		Description: "Cancel a dev session: removes the worktree and local branch without pushing or creating a PR. All uncommitted changes are lost.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"session": {
					"type": "string",
					"description": "Branch name of the session to cancel. Optional if only one session is active."
				}
			}
		}`),
	}
}

type devCancelArgs struct {
	Session string `json:"session"`
}

func devCancelHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a devCancelArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		sess, err := deps.Store.ResolveSession(ctx, a.Session)
		if err != nil {
			return nil, err
		}

		if cleanupErr := worktreeRemove(sess.RepoDir, sess.WorktreeDir, sess.Branch); cleanupErr != nil {
			return nil, fmt.Errorf("cleanup: %w", cleanupErr)
		}

		if err := deps.Store.DeleteSession(ctx, sess.Branch); err != nil {
			return nil, fmt.Errorf("delete session: %w", err)
		}

		result := map[string]any{
			"branch":  sess.Branch,
			"message": fmt.Sprintf("Dev session for branch %q cancelled. Worktree and local branch removed.", sess.Branch),
		}
		return json.Marshal(result)
	}
}
