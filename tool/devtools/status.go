package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/mcp"
)

func devStatusDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "dev_status",
		Description: "Show the status of active dev sessions — branch, changed files, commit log. If multiple sessions are active and no session is specified, lists all sessions.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"session": {
					"type": "string",
					"description": "Branch name of the session to check. Optional if only one session is active."
				}
			}
		}`),
	}
}

type devStatusArgs struct {
	Session string `json:"session"`
}

func devStatusHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a devStatusArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		sessions, err := deps.Store.ListSessions(ctx)
		if err != nil {
			return nil, err
		}
		if len(sessions) == 0 {
			return json.Marshal(map[string]any{
				"message": "No active dev sessions.",
			})
		}

		// If no specific session requested and multiple exist, list all.
		if a.Session == "" && len(sessions) > 1 {
			var summaries []map[string]any
			for branch, sess := range sessions {
				summaries = append(summaries, map[string]any{
					"branch":      branch,
					"worktree":    sess.WorktreeDir,
					"age":         time.Since(sess.CreatedAt).Truncate(time.Minute).String(),
				})
			}
			return json.Marshal(map[string]any{
				"sessions": summaries,
				"message":  "Multiple active sessions. Specify a session (branch name) for details.",
			})
		}

		sess, err := deps.Store.ResolveSession(ctx, a.Session)
		if err != nil {
			return nil, err
		}

		status, statusErr := gitStatus(sess.WorktreeDir)
		if statusErr != nil {
			status = "error: " + statusErr.Error()
		}

		log, logErr := gitLog(sess.WorktreeDir)
		if logErr != nil {
			log = "error: " + logErr.Error()
		}

		diffStat, diffErr := gitDiffStat(sess.WorktreeDir)
		if diffErr != nil {
			diffStat = "error: " + diffErr.Error()
		}

		result := map[string]any{
			"branch":        sess.Branch,
			"worktree_dir":  sess.WorktreeDir,
			"age":           time.Since(sess.CreatedAt).Truncate(time.Minute).String(),
			"uncommitted":   status,
			"commit_log":    log,
			"diff_stat":     diffStat,
		}
		return json.Marshal(result)
	}
}
