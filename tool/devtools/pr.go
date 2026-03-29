package devtools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/mcp"
)

const ToolPR = "dev_pr"

func devPRDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolPR,
		Description: "Commit, push, and open (or update) a PR — keeping the dev session alive for continued iteration. " +
			"Use this instead of dev_end when you expect to make more changes based on feedback. " +
			"Calling dev_pr again after making further changes will push additional commits to the same PR. " +
			"Call dev_end when the PR is merged or you're done with the session.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"session": {
					"type": "string",
					"description": "Branch name of the session. Optional if only one session is active."
				},
				"title": {
					"type": "string",
					"description": "Commit message and PR title."
				},
				"body": {
					"type": "string",
					"description": "PR description body (markdown). Only used when creating the PR for the first time."
				}
			},
			"required": ["title"]
		}`),
	}
}

type devPRArgs struct {
	Session string `json:"session"`
	Title   string `json:"title"`
	Body    string `json:"body"`
}

func devPRHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a devPRArgs
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

		repoURL, err := deps.Store.GetRepoURL(ctx)
		if err != nil {
			return nil, err
		}
		token, err := deps.SecretStore.Get(ctx, githubTokenKey)
		if err != nil {
			return nil, fmt.Errorf("read github token: %w", err)
		}

		committed, err := gitAddAndCommit(sess.WorktreeDir, a.Title)
		if err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}

		if err := gitPush(sess.WorktreeDir, sess.Branch, token, repoURL); err != nil {
			return nil, fmt.Errorf("push: %w", err)
		}

		// Find an existing PR or create a new one.
		prURL, err := ghPRFind(sess.WorktreeDir, sess.Branch, token)
		if err != nil {
			prURL = ""
		}

		if prURL == "" {
			body := a.Body
			if body == "" {
				body = a.Title
			}
			prURL, err = ghPRCreate(sess.WorktreeDir, sess.Branch, a.Title, body, token)
			if err != nil {
				return nil, fmt.Errorf("create PR: %w", err)
			}
		}

		result := map[string]any{
			"branch":    sess.Branch,
			"committed": committed,
			"pr_url":    prURL,
			"message": fmt.Sprintf(
				"PR open at %s. Session is still active — make more changes and call dev_pr again to push updates, or call dev_end to close the session.",
				prURL,
			),
		}
		return json.Marshal(result)
	}
}
