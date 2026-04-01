package repotools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"tclaw/internal/mcp"
)

const ToolRemove = "repo_remove"

func repoRemoveDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolRemove,
		Description: "Stop tracking a repo and clean up all cached data (clone directory, store entry).",
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

		// Remove the entire repo directory (the clone).
		if err := os.RemoveAll(tracked.RepoDir); err != nil {
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
