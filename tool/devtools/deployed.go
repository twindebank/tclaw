package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/mcp"
)

func devDeployedDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "dev_deployed",
		Description: "Show what's currently deployed — the deployed commit hash, the latest origin/main commit, and whether a deploy is needed.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	}
}

func devDeployedHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
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

		repoDir := fmt.Sprintf("%s/repo", deps.UserDir)
		if err := cloneOrFetch(repoDir, repoURL, token); err != nil {
			return nil, fmt.Errorf("fetch: %w", err)
		}

		deployedCommit, err := deps.Store.GetDeployedCommit(ctx)
		if err != nil {
			return nil, err
		}

		mainHead, err := gitHeadCommitRef(repoDir, "origin/main")
		if err != nil {
			return nil, err
		}

		// Extract just the short hash from "abc1234 commit subject".
		mainShort := strings.SplitN(mainHead, " ", 2)[0]

		result := map[string]any{
			"latest_commit": mainHead,
		}

		if deployedCommit == "" {
			result["deployed_commit"] = "unknown (no deploy recorded)"
			result["up_to_date"] = false
			result["message"] = "No deploy has been recorded yet. The latest commit on origin/main is: " + mainHead
		} else {
			result["deployed_commit"] = deployedCommit
			upToDate := deployedCommit == mainShort
			result["up_to_date"] = upToDate

			if upToDate {
				result["message"] = "Deployed commit matches origin/main — up to date."
			} else {
				commitLog, logErr := gitLogRange(repoDir, deployedCommit, "origin/main")
				if logErr != nil {
					commitLog = "error: " + logErr.Error()
				}
				commitCount := 0
				if commitLog != "" {
					commitCount = len(strings.Split(commitLog, "\n"))
				}
				result["commits_behind"] = commitCount
				result["commits_since_deploy"] = commitLog
				result["message"] = fmt.Sprintf("Deployed commit %s is %d commit(s) behind origin/main.", deployedCommit, commitCount)
			}
		}

		return json.Marshal(result)
	}
}
