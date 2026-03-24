package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tclaw/dev"
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

		// Try fetching the live /version endpoint — works for any deploy, not just tool-triggered ones.
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

// fetchLiveVersion hits the running app's /version endpoint and returns the
// commit hash. Returns empty string if the app URL is not configured or the
// request fails — callers should fall back to the stored deployed commit.
// defaultAppURL is the fallback when the store doesn't have an app URL
// (e.g. when all deploys happened locally via `go run . deploy`).
const defaultAppURL = "https://tclaw.fly.dev"

// fetchLiveVersion hits the running app's /version endpoint. Returns the
// commit hash and nil on success. On failure, returns "" and the error —
// callers should include the error in their response so failures are visible.
func fetchLiveVersion(ctx context.Context, store *dev.Store) (string, error) {
	appURL, err := store.GetAppURL(ctx)
	if err != nil {
		slog.Warn("failed to read app URL from store, using default", "err", err)
	}
	if appURL == "" {
		appURL = defaultAppURL
	}

	versionURL := appURL + "/version"
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		return "", fmt.Errorf("create version request for %s: %w", versionURL, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", versionURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("version endpoint %s returned %d", versionURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", fmt.Errorf("read version response from %s: %w", versionURL, err)
	}
	return strings.TrimSpace(string(body)), nil
}
