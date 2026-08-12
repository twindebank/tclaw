package repotools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/internal/mcp"
	"tclaw/internal/repo"
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
			Description    string `json:"description,omitempty"`
			Managed        bool   `json:"managed,omitempty"`
			Access         string `json:"access"`
			Credential     string `json:"credential,omitempty"`
			AccessEndsAt   string `json:"access_ends_at,omitempty"`
		}

		var infos []repoInfo
		for _, r := range repos {
			// Repos scoped to other channels are omitted entirely — this
			// channel has no clone mounted for them either.
			if _, ok := deps.visibleTo(r); !ok {
				continue
			}
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
				Description:    r.Description,
				Managed:        r.Managed,
				Access:         string(r.EffectiveAccess(time.Now())),
				Credential:     r.Credential,
				AccessEndsAt:   accessEndsAt(r),
			})
		}

		if len(infos) == 0 {
			result := map[string]any{
				"repos":   []any{},
				"message": "No tracked repos available on this channel. Use repo_add to start monitoring a repo.",
			}
			return json.Marshal(result)
		}

		result := map[string]any{
			"repos":   infos,
			"message": fmt.Sprintf("%d tracked repo(s).", len(infos)),
		}
		return json.Marshal(result)
	}
}

// accessEndsAt renders a pending downgrade, so the agent can see that push
// access is temporary rather than discovering it has lapsed mid-task.
func accessEndsAt(r repo.TrackedRepo) string {
	if r.DropToReadOnlyAt.IsZero() || !r.EffectiveAccess(time.Now()).AllowsPush() {
		return ""
	}
	return r.DropToReadOnlyAt.Format(time.RFC3339)
}
