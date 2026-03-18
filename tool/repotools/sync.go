package repotools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"tclaw/mcp"
)

const (
	defaultSyncDepth = 50

	// githubTokenKey matches the devtools key so the same token is shared.
	githubTokenKey = "github_token"
)

func repoSyncDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "repo_sync",
		Description: "Fetch the latest from a tracked repo and report what's new since the last sync. Updates the read-only checkout for file exploration via Read/Grep/Glob.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Repo name. Optional if only one repo is tracked."
				},
				"depth": {
					"type": "integer",
					"description": "Number of commits to fetch (shallow clone depth). Defaults to 50."
				}
			}
		}`),
	}
}

type repoSyncArgs struct {
	Name  string `json:"name"`
	Depth int    `json:"depth"`
}

func repoSyncHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a repoSyncArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		tracked, err := deps.Store.Resolve(ctx, a.Name)
		if err != nil {
			return nil, err
		}

		depth := a.Depth
		if depth <= 0 {
			depth = defaultSyncDepth
		}

		// Ensure directories exist — they may be gone if the volume was
		// wiped while the store entries survived.
		if err := os.MkdirAll(tracked.RepoDir, 0o755); err != nil {
			return nil, fmt.Errorf("create repo dir: %w", err)
		}

		// Read GitHub token for private repos. Public repos work without one.
		token, _ := deps.SecretStore.Get(ctx, githubTokenKey)

		if err := shallowCloneOrFetch(tracked.RepoDir, tracked.URL, tracked.Branch, token, depth); err != nil {
			return nil, fmt.Errorf("fetch: %w", err)
		}

		// Get new HEAD.
		newHead, err := headCommitSHA(tracked.RepoDir, tracked.Branch)
		if err != nil {
			return nil, fmt.Errorf("read HEAD: %w", err)
		}

		// Compute what's new since last sync.
		var newCommitLog string
		var newCommitCount int
		if tracked.LastSeenCommit != "" && tracked.LastSeenCommit != newHead {
			logOutput, err := commitLogSince(tracked.RepoDir, tracked.Branch, tracked.LastSeenCommit, depth)
			if err != nil {
				return nil, fmt.Errorf("commit log: %w", err)
			}
			newCommitLog = logOutput
			if logOutput != "" {
				newCommitCount = len(strings.Split(logOutput, "\n"))
			}
		} else if tracked.LastSeenCommit == "" {
			// First sync — show recent commits as context.
			logOutput, err := commitLogRecent(tracked.RepoDir, tracked.Branch, 10)
			if err != nil {
				return nil, fmt.Errorf("commit log: %w", err)
			}
			newCommitLog = logOutput
			if logOutput != "" {
				newCommitCount = len(strings.Split(logOutput, "\n"))
			}
		}

		// Update the read-only checkout.
		checkout, err := readOnlyCheckout(tracked.RepoDir, tracked.WorktreeDir, tracked.Branch)
		if err != nil {
			return nil, fmt.Errorf("checkout: %w", err)
		}

		// Persist updated cursor.
		tracked.LastSeenCommit = newHead
		tracked.LastSyncedAt = time.Now()
		if err := deps.Store.Put(ctx, *tracked); err != nil {
			return nil, fmt.Errorf("save repo: %w", err)
		}

		message := fmt.Sprintf("Repo %q synced. %d new commit(s). Checkout %s (%d files, %s). Explore files at %s",
			tracked.Name, newCommitCount, checkout.Action, checkout.FileCount, tracked.WorktreeDir, tracked.WorktreeDir)
		if newCommitCount == 0 {
			message = fmt.Sprintf("Repo %q synced — no new commits since last check. Checkout %s (%d files). Explore files at %s",
				tracked.Name, checkout.Action, checkout.FileCount, tracked.WorktreeDir)
		}

		result := map[string]any{
			"name":                tracked.Name,
			"new_commit_count":    newCommitCount,
			"new_commits":         newCommitLog,
			"head_commit":         newHead,
			"worktree_dir":        tracked.WorktreeDir,
			"checkout_action":     checkout.Action,
			"checkout_file_count": checkout.FileCount,
			"message":             message,
		}
		return json.Marshal(result)
	}
}
