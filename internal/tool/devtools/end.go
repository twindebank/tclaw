package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tclaw/internal/dev"
	"tclaw/internal/mcp"
)

const ToolEnd = "dev_end"

func devEndDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolEnd,
		Description: "Tear down a dev session. Commits any uncommitted changes, pushes, and cleans up the worktree. " +
			"Preferred workflow: use dev_pr to open/update the PR and iterate, then call dev_end when the PR is merged or you're done. " +
			"dev_end opens a PR only if the branch doesn't already have one — it will NOT duplicate a PR that is already open or merged, so ending a session after its PR was merged just cleans up the worktree. " +
			"If PR creation fails after a successful push, the session is preserved — call dev_end again to retry. " +
			"Sessions are scoped to the channel that started them: you can only end a session started in THIS channel, and if this channel has just one, omit 'session' — it resolves automatically. " +
			"Note: the 'session' parameter is only for disambiguating between multiple sessions in this channel — it is NOT the way to resume a session.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"session": {
					"type": "string",
					"description": "Branch name of the session to end. Optional if this channel has only one active session. Must be a session started in this channel."
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

		sess, err := deps.Store.ResolveSession(ctx, dev.ResolveParams{
			Session: a.Session,
			Channel: deps.activeChannelName(),
		})
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

		// Check for an existing PR across all states. A branch whose PR is already
		// OPEN or MERGED must NOT get a second PR — creating one after a merge is
		// exactly the duplicate-PR bug. Only a branch with no PR (or a PR that was
		// closed without merging) gets a fresh PR here.
		pr, err := ghPRFind(sess.WorktreeDir, sess.Branch, token)
		if err != nil {
			// Non-fatal: gh might not be available or the repo may not be on GitHub.
			slog.Warn("failed to check for existing PR", "branch", sess.Branch, "err", err)
			pr = prInfo{}
		}

		prURL := pr.URL
		alreadyMerged := pr.State == prStateMerged

		if shouldCreatePRForEnd(pr.State) {
			// No usable PR (none, or closed-without-merge) — create one.
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

		message := fmt.Sprintf("Dev session ended. Branch %q pushed and worktree cleaned up.", sess.Branch)
		if alreadyMerged {
			message = fmt.Sprintf("Dev session ended. Branch %q was already merged (%s) — no new PR created; worktree cleaned up.", sess.Branch, prURL)
		}
		result := map[string]any{
			"branch":    sess.Branch,
			"committed": committed,
			"pr_url":    prURL,
			"message":   message,
		}
		return json.Marshal(result)
	}
}
