package repotools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tclaw/mcp"
	"tclaw/repo"
)

func repoAddDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "repo_add",
		Description: "Register a remote git repo for read-only monitoring. Does not clone — use repo_sync after adding to fetch the code.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Short alias for the repo (e.g. 'nanoclaw', 'claude-code'). Alphanumeric and hyphens only."
				},
				"url": {
					"type": "string",
					"description": "GitHub repo HTTPS URL (e.g. https://github.com/user/repo)."
				},
				"branch": {
					"type": "string",
					"description": "Branch to track. Defaults to 'main'."
				}
			},
			"required": ["name", "url"]
		}`),
	}
}

type repoAddArgs struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Branch string `json:"branch"`
}

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

func repoAddHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a repoAddArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Name == "" {
			return nil, fmt.Errorf("name is required")
		}
		if a.URL == "" {
			return nil, fmt.Errorf("url is required")
		}
		if !validName.MatchString(a.Name) || len(a.Name) > 64 {
			return nil, fmt.Errorf("name must be alphanumeric/hyphens, max 64 chars")
		}
		if !strings.HasPrefix(a.URL, "https://") {
			return nil, fmt.Errorf("url must be an HTTPS URL")
		}

		branch := a.Branch
		if branch == "" {
			branch = "main"
		}

		// Check for duplicate.
		existing, err := deps.Store.Get(ctx, a.Name)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, fmt.Errorf("repo %q already tracked — use repo_remove first to re-add", a.Name)
		}

		repoDir := filepath.Join(deps.UserDir, "repos", a.Name, "bare")
		checkoutDir := filepath.Join(deps.UserDir, "repos", a.Name, "checkout")

		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			return nil, fmt.Errorf("create repo dir: %w", err)
		}
		if err := os.MkdirAll(checkoutDir, 0o755); err != nil {
			return nil, fmt.Errorf("create checkout dir: %w", err)
		}

		tracked := repo.TrackedRepo{
			Name:        a.Name,
			URL:         a.URL,
			Branch:      branch,
			RepoDir:     repoDir,
			WorktreeDir: checkoutDir,
			AddedAt:     time.Now(),
		}
		if err := deps.Store.Put(ctx, tracked); err != nil {
			return nil, fmt.Errorf("save repo: %w", err)
		}

		result := map[string]any{
			"name":    a.Name,
			"url":     a.URL,
			"branch":  branch,
			"message": fmt.Sprintf("Repo %q registered. Run repo_sync to fetch and explore.", a.Name),
		}
		return json.Marshal(result)
	}
}
