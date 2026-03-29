package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"tclaw/mcp"
)

const ToolDeploy = "deploy"

func deployDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolDeploy,
		Description: `Check deployment status — what's deployed vs what's on main.

Deployments happen automatically via GitHub Actions CI when code is pushed
to main. This tool shows whether production is up to date, what commits
are pending, and the CI run status. It does NOT deploy — that's CI's job.

To deploy: merge a PR (or push to main) and CI deploys automatically.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	}
}

func deployHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		repoURL, err := deps.Store.GetRepoURL(ctx)
		if err != nil {
			return nil, err
		}
		if repoURL == "" {
			return nil, fmt.Errorf("no repo URL configured — run dev_start once to configure the repo")
		}

		token, err := deps.SecretStore.Get(ctx, githubTokenKey)
		if err != nil {
			return nil, fmt.Errorf("read github token: %w", err)
		}

		repoDir := fmt.Sprintf("%s/repo", deps.UserDir)
		if err := cloneOrFetch(repoDir, repoURL, token); err != nil {
			return nil, fmt.Errorf("fetch: %w", err)
		}

		// Get the currently deployed commit from the live /version endpoint.
		deployedCommit, err := deps.Store.GetDeployedCommit(ctx)
		if err != nil {
			return nil, err
		}
		liveCommit, liveErr := fetchLiveVersion(ctx, deps.Store)
		if liveErr != nil {
			slog.Warn("failed to fetch live version", "err", liveErr)
		}
		if liveCommit != "" {
			deployedCommit = liveCommit
		}

		// Get origin/main HEAD.
		cmd := exec.Command("git", "-C", repoDir, "rev-parse", "--short", "origin/main")
		mainShortOut, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("rev-parse origin/main: %s: %w", string(mainShortOut), err)
		}
		mainShort := strings.TrimSpace(string(mainShortOut))

		mainHead, err := gitHeadCommitRef(repoDir, "origin/main")
		if err != nil {
			return nil, err
		}

		result := map[string]any{
			"target_commit": mainHead,
		}

		if deployedCommit == "" {
			result["deployed_commit"] = "unknown"
			if liveErr != nil {
				result["version_error"] = liveErr.Error()
			}
			result["message"] = "Could not determine deployed version. Push to main to trigger a CI deploy."
		} else if deployedCommit == mainShort || strings.HasPrefix(mainHead, deployedCommit) {
			result["deployed_commit"] = deployedCommit
			result["up_to_date"] = true
			result["message"] = "Production is up to date with main."
		} else {
			result["deployed_commit"] = deployedCommit

			commitLog, logErr := gitLogRange(repoDir, deployedCommit, "origin/main")
			if logErr != nil {
				commitLog = "error: " + logErr.Error()
			}
			diffStat, diffErr := gitDiffStatRange(repoDir, deployedCommit, "origin/main")
			if diffErr != nil {
				diffStat = "error: " + diffErr.Error()
			}

			commitCount := 0
			if commitLog != "" {
				commitCount = len(strings.Split(commitLog, "\n"))
			}

			result["commits_since_deploy"] = commitCount
			result["commit_log"] = commitLog
			result["changed_files"] = diffStat
			result["message"] = fmt.Sprintf("%d commit(s) behind main. Push to main or merge a PR to trigger CI deploy.", commitCount)
		}

		return json.Marshal(result)
	}
}
