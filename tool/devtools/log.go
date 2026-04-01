package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"tclaw/mcp"
)

const ToolLog = "dev_log"

func devLogDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolLog,
		Description: "Show recent commit history on origin/main. Useful for reviewing what's been merged and comparing against the deployed version.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"count": {
					"type": "integer",
					"description": "Number of commits to show. Defaults to 20."
				}
			}
		}`),
	}
}

type devLogArgs struct {
	Count int `json:"count"`
}

func devLogHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a devLogArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		count := a.Count
		if count <= 0 {
			count = 20
		}

		repoURL, err := deps.Store.GetRepoURL(ctx)
		if err != nil {
			return nil, err
		}
		if repoURL == "" {
			return nil, fmt.Errorf("no repo URL configured — run dev_start first to set up the repo")
		}

		token, err := deps.SecretStore.Get(ctx, githubTokenKey)
		if err != nil {
			return nil, fmt.Errorf("read github token: %w", err)
		}

		repoDir, err := repoDirForURL(deps.UserDir, repoURL)
		if err != nil {
			return nil, fmt.Errorf("invalid repo URL: %w", err)
		}
		if err := cloneOrFetch(repoDir, repoURL, token); err != nil {
			return nil, fmt.Errorf("fetch: %w", err)
		}

		cmd := exec.Command("git", "-C", repoDir, "log", "--oneline", fmt.Sprintf("-%d", count), "origin/main")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("git log: %s: %w", string(out), err)
		}

		// Mark the deployed commit in the log if we know it.
		commitLog := strings.TrimSpace(string(out))
		deployedCommit, deployErr := deps.Store.GetDeployedCommit(ctx)
		if deployErr != nil {
			return nil, deployErr
		}

		result := map[string]any{
			"commit_log": commitLog,
		}
		if deployedCommit != "" {
			result["deployed_commit"] = deployedCommit
		}

		return json.Marshal(result)
	}
}
