package repotools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/internal/mcp"
)

const ToolLog = "repo_log"

func repoLogDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolLog,
		Description: "Show commit history for a tracked repo. Optionally include diffstat per commit.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Repo name. Optional if only one repo is tracked."
				},
				"count": {
					"type": "integer",
					"description": "Number of commits to show. Defaults to 20."
				},
				"since": {
					"type": "string",
					"description": "Only show commits after this date (ISO 8601, e.g. '2025-01-15')."
				},
				"diff": {
					"type": "boolean",
					"description": "Include diffstat (files changed) per commit."
				}
			}
		}`),
	}
}

type repoLogArgs struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Since string `json:"since"`
	Diff  bool   `json:"diff"`
}

func repoLogHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a repoLogArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		tracked, err := deps.Store.Resolve(ctx, a.Name)
		if err != nil {
			return nil, err
		}

		count := a.Count
		if count <= 0 {
			count = 20
		}

		if tracked.LastSyncedAt.IsZero() {
			return nil, fmt.Errorf("repo %q has not been synced yet — run repo_sync first", tracked.Name)
		}

		output, err := commitLogDetailed(tracked.RepoDir, tracked.Branch, count, a.Since, a.Diff)
		if err != nil {
			return nil, fmt.Errorf("git log: %w", err)
		}

		result := map[string]any{
			"name": tracked.Name,
			"log":  output,
		}
		return json.Marshal(result)
	}
}
