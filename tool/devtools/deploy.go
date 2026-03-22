package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tclaw/libraries/credentialerror"

	"tclaw/mcp"
)

func deployDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "deploy",
		Description: "Deploy the application to Fly.io. Call without confirm=true to preview what will be deployed (commit log, changed files). Call with confirm=true to execute the deploy. Only deploys from main.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"confirm": {
					"type": "boolean",
					"description": "Set to true to execute the deploy after reviewing the preview. Omit or false to get a preview."
				},
				"fly_api_token": {
					"type": "string",
					"description": "Fly.io API token for deploy access. Only needed on first use — stored encrypted for subsequent calls."
				}
			}
		}`),
	}
}

type deployArgs struct {
	Confirm     bool   `json:"confirm"`
	FlyAPIToken string `json:"fly_api_token"`
}

func deployHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a deployArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		// We need the repo to exist for deploy preview/execution.
		repoURL, err := deps.Store.GetRepoURL(ctx)
		if err != nil {
			return nil, err
		}
		if repoURL == "" {
			return nil, fmt.Errorf("no repo URL configured — run dev_start once to configure the repo, then you can deploy independently")
		}

		token, err := deps.SecretStore.Get(ctx, githubTokenKey)
		if err != nil {
			return nil, fmt.Errorf("read github token: %w", err)
		}

		repoDir := fmt.Sprintf("%s/repo", deps.UserDir)
		if err := cloneOrFetch(repoDir, repoURL, token); err != nil {
			return nil, fmt.Errorf("fetch: %w", err)
		}

		// Get current deployed commit.
		deployedCommit, err := deps.Store.GetDeployedCommit(ctx)
		if err != nil {
			return nil, err
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

		if !a.Confirm {
			// Preview mode.
			result := map[string]any{
				"target_commit": mainHead,
			}

			if deployedCommit == "" {
				result["deployed_commit"] = "unknown (first deploy)"
				result["message"] = "First deploy — no previous version to compare. Call deploy with confirm=true to proceed."
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

				if commitCount == 0 {
					result["message"] = "Already up to date — nothing new to deploy."
				} else {
					result["message"] = fmt.Sprintf("%d commit(s) to deploy. Call deploy with confirm=true to proceed.", commitCount)
				}
			}

			return json.Marshal(result)
		}

		// Resolve and persist Fly API token.
		flyToken, err := deps.SecretStore.Get(ctx, flyTokenKey)
		if err != nil {
			return nil, fmt.Errorf("read fly api token: %w", err)
		}
		if a.FlyAPIToken != "" {
			flyToken = a.FlyAPIToken
			if err := deps.SecretStore.Set(ctx, flyTokenKey, flyToken); err != nil {
				return nil, fmt.Errorf("persist fly api token: %w", err)
			}
		}
		if flyToken == "" {
			return nil, credentialerror.New(
				"Fly.io Configuration",
				"A Fly.io API token is needed for deployment",
				credentialerror.Field{Key: flyTokenKey, Label: "Fly.io API Token", Description: "Create with: fly tokens create deploy -x 999999h"},
			)
		}

		// Create a temporary checkout from the bare repo so fly can find the Dockerfile.
		// The bare repo has no working tree — fly deploy needs actual files.
		const appName = "tclaw"
		checkoutDir := repoDir + "-deploy-checkout"
		if err := os.RemoveAll(checkoutDir); err != nil {
			return nil, fmt.Errorf("clean deploy checkout: %w", err)
		}
		// Prune stale worktree refs so git doesn't reject the re-add with
		// "already registered" after a volume wipe and bare repo re-clone.
		pruneCmd := exec.Command("git", "-C", repoDir, "worktree", "prune")
		if out, err := pruneCmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git worktree prune: %s: %w", string(out), err)
		}
		cmd = exec.Command("git", "-c", "core.hooksPath=/dev/null", "-C", repoDir, "worktree", "add", "--detach", checkoutDir, "origin/main")
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("create deploy checkout: %s: %w", string(out), err)
		}
		defer func() {
			// Clean up the temporary worktree.
			rmCmd := exec.Command("git", "-C", repoDir, "worktree", "remove", "--force", checkoutDir)
			if out, err := rmCmd.CombinedOutput(); err != nil {
				slog.Warn("failed to remove deploy worktree", "err", err, "output", string(out))
			}
		}()

		// tclaw.yaml is gitignored, so the git checkout won't have it. Copy the
		// real config into the checkout so the Dockerfile COPY finds it — the
		// build fails without it.
		if deps.ConfigPath == "" {
			return nil, fmt.Errorf("config path is required for deploy — tclaw.yaml must be available")
		}
		configData, readErr := os.ReadFile(deps.ConfigPath)
		if readErr != nil {
			return nil, fmt.Errorf("read config for deploy: %w", readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(checkoutDir, "tclaw.yaml"), configData, 0o600); writeErr != nil {
			return nil, fmt.Errorf("copy config to checkout: %w", writeErr)
		}

		// fly deploy reads FLY_API_TOKEN from env (no stdin alternative). The token is
		// only visible to this subprocess — the claude CLI runs in a separate PID namespace
		// (--unshare-pid in sandbox.go) and cannot read /proc/<pid>/environ.
		cmd = exec.Command("fly", "deploy", "--remote-only", "-a", appName)
		cmd.Dir = checkoutDir
		cmd.Env = append(cmd.Env, "FLY_API_TOKEN="+flyToken, "PATH="+os.Getenv("PATH"), "HOME="+os.Getenv("HOME"))
		deployOut, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("fly deploy failed: %s: %w", string(deployOut), err)
		}

		// Record the deployed commit and app URL. Non-fatal since deploy already succeeded.
		if setErr := deps.Store.SetDeployedCommit(ctx, mainShort); setErr != nil {
			slog.Warn("failed to record deployed commit", "commit", mainShort, "err", setErr)
		}
		appURL := "https://" + appName + ".fly.dev"
		if setErr := deps.Store.SetAppURL(ctx, appURL); setErr != nil {
			slog.Warn("failed to record app url", "url", appURL, "err", setErr)
		}

		result := map[string]any{
			"deployed_commit": mainShort,
			"output":          strings.TrimSpace(string(deployOut)),
			"message":         "Deploy complete.",
		}
		return json.Marshal(result)
	}
}

// gitHeadCommitRef returns the short hash and subject of a ref.
func gitHeadCommitRef(repoDir string, ref string) (string, error) {
	cmd := exec.Command("git", "-C", repoDir, "log", "-1", "--format=%h %s", ref)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git log %s: %s: %w", ref, string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}
