package repotools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/mcp"
)

const ToolList = "repo_list"

func repoListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolList,
		Description: "List all tracked repos and their status (last synced, branch, worktree path).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	}
}

func repoListHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		repos, err := deps.Store.List(ctx)
		if err != nil {
			return nil, err
		}

		if len(repos) == 0 {
			result := map[string]any{
				"repos":   []any{},
				"message": "No tracked repos. Use repo_add to start monitoring a repo.",
			}
			return json.Marshal(result)
		}

		type repoInfo struct {
			Name           string `json:"name"`
			URL            string `json:"url"`
			Branch         string `json:"branch"`
			LastSynced     string `json:"last_synced"`
			LastSeenCommit string `json:"last_seen_commit"`
			RepoDir        string `json:"repo_dir"`
			Age            string `json:"age"`
		}

		var infos []repoInfo
		for _, r := range repos {
			lastSynced := "never"
			if !r.LastSyncedAt.IsZero() {
				lastSynced = r.LastSyncedAt.Format(time.RFC3339)
			}
			commit := r.LastSeenCommit
			if commit == "" {
				commit = "(not synced)"
			} else if len(commit) > 12 {
				commit = commit[:12]
			}

			infos = append(infos, repoInfo{
				Name:           r.Name,
				URL:            r.URL,
				Branch:         r.Branch,
				LastSynced:     lastSynced,
				LastSeenCommit: commit,
				RepoDir:        r.RepoDir,
				Age:            time.Since(r.AddedAt).Truncate(time.Minute).String(),
			})
		}

		result := map[string]any{
			"repos":   infos,
			"message": fmt.Sprintf("%d tracked repo(s).", len(infos)),
		}
		return json.Marshal(result)
	}
}
