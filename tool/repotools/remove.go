package repotools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"tclaw/mcp"
)

func repoRemoveDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "repo_remove",
		Description: "Stop tracking a repo and clean up all cached data (bare repo, checkout directory, store entry).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Repo name to remove."
				}
			},
			"required": ["name"]
		}`),
	}
}

type repoRemoveArgs struct {
	Name string `json:"name"`
}

func repoRemoveHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a repoRemoveArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Name == "" {
			return nil, fmt.Errorf("name is required")
		}

		tracked, err := deps.Store.Get(ctx, a.Name)
		if err != nil {
			return nil, err
		}
		if tracked == nil {
			return nil, fmt.Errorf("no tracked repo named %q", a.Name)
		}

		// Remove worktree from git's tracking before deleting files.
		if tracked.WorktreeDir != "" {
			if err := worktreeRemove(tracked.RepoDir, tracked.WorktreeDir); err != nil {
				// Non-fatal — the worktree may not exist yet if never synced.
				slog.Debug("failed to remove git worktree (may not exist)", "name", a.Name, "err", err)
			}
		}

		// Remove the entire repo directory (bare/ + checkout/).
		// RepoDir is <userDir>/repos/<name>/bare — go up one level to get repos/<name>/.
		repoParent := filepath.Dir(tracked.RepoDir)
		if err := os.RemoveAll(repoParent); err != nil {
			return nil, fmt.Errorf("remove repo files: %w", err)
		}

		if err := deps.Store.Delete(ctx, a.Name); err != nil {
			return nil, fmt.Errorf("delete from store: %w", err)
		}

		result := map[string]any{
			"name":    a.Name,
			"message": fmt.Sprintf("Repo %q removed. All cached data cleaned up.", a.Name),
		}
		return json.Marshal(result)
	}
}
