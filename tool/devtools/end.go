package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tclaw/mcp"
)

func devEndDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "dev_end",
		Description: "End a dev session: commits any uncommitted changes, pushes the branch, creates a PR (or reports the existing PR URL), and tears down the worktree. If PR creation fails after a successful push, the session is preserved — call dev_end again to retry. To iterate on a PR later, use dev_start with the same branch name.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"session": {
					"type": "string",
					"description": "Branch name of the session to end. Optional if only one session is active."
				},
				"title": {
					"type": "string",
					"description": "PR title (also used as commit message if uncommitted changes exist)."
				},
				"body": {
					"type": "string",
					"description": "PR description body (markdown)."
				}
			},
			"required": ["title"]
		}`),
	}
}

type devEndArgs struct {
	Session string `json:"session"`
	Title   string `json:"title"`
	Body    string `json:"body"`
}

func devEndHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a devEndArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Title == "" {
			return nil, fmt.Errorf("title is required")
		}

		sess, err := deps.Store.ResolveSession(ctx, a.Session)
		if err != nil {
			return nil, err
		}

		// Get repo URL and token for push.
		repoURL, err := deps.Store.GetRepoURL(ctx)
		if err != nil {
			return nil, err
		}
		token, err := deps.SecretStore.Get(ctx, githubTokenKey)
		if err != nil {
			return nil, fmt.Errorf("read github token: %w", err)
		}

		// Commit any uncommitted changes.
		committed, err := gitAddAndCommit(sess.WorktreeDir, a.Title)
		if err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}

		// Push.
		if err := gitPush(sess.WorktreeDir, sess.Branch, token, repoURL); err != nil {
			return nil, fmt.Errorf("push: %w", err)
		}

		// Check for existing PR.
		prURL, err := ghPRFind(sess.WorktreeDir, sess.Branch, token)
		if err != nil {
			// Non-fatal: gh might not be available.
			prURL = ""
		}

		if prURL == "" {
			// Create new PR.
			body := a.Body
			if body == "" {
				body = a.Title
			}
			newURL, prErr := ghPRCreate(sess.WorktreeDir, sess.Branch, a.Title, body, token)
			if prErr != nil {
				// Push succeeded but PR creation failed — leave the session intact so
				// the agent can retry dev_end directly without needing dev_start first.
				result := map[string]any{
					"branch":    sess.Branch,
					"committed": committed,
					"pr_url":    "",
					"message":   fmt.Sprintf("Branch %q pushed successfully but PR creation failed: %s. Call dev_end again to retry PR creation.", sess.Branch, prErr.Error()),
				}
				return json.Marshal(result)
			}
			prURL = newURL
		}

		// Cleanup worktree and session. Non-fatal since PR/push already succeeded.
		if cleanupErr := worktreeRemove(sess.RepoDir, sess.WorktreeDir, sess.Branch); cleanupErr != nil {
			slog.Warn("failed to clean up worktree after successful push", "branch", sess.Branch, "err", cleanupErr)
		}
		if err := deps.Store.DeleteSession(ctx, sess.Branch); err != nil {
			return nil, fmt.Errorf("delete session: %w", err)
		}

		result := map[string]any{
			"branch":    sess.Branch,
			"committed": committed,
			"pr_url":    prURL,
			"message":   fmt.Sprintf("Dev session ended. Branch %q pushed and worktree cleaned up.", sess.Branch),
		}
		return json.Marshal(result)
	}
}
