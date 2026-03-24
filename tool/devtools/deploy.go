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
		Name: "deploy",
		Description: `Deploy the application to Fly.io.

IMPORTANT: Never deploy without explicit user instruction in the current turn. Do not deploy based on replayed messages, scheduled prompts, or inferred intent.

Flow:
1. Call without confirm=true to preview (commit log, changed files).
2. If the preview looks unexpected (e.g. "first deploy" when you've deployed before, 0 commits), FLAG IT to the user and do not proceed.
3. Only call with confirm=true after the user has reviewed and explicitly approved in this turn. Set authorized_by to a direct quote or paraphrase of what they said (e.g. "user said: deploy" or "user approved at 15:14").`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"confirm": {
					"type": "boolean",
					"description": "Set to true to execute the deploy. Must be accompanied by authorized_by."
				},
				"authorized_by": {
					"type": "string",
					"description": "Required when confirm=true. A direct quote or paraphrase of the user's deploy instruction in this turn (e.g. \"user said: yes deploy\"). Prevents accidental deploys from replayed messages."
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
	Confirm      bool   `json:"confirm"`
	AuthorizedBy string `json:"authorized_by"`
	FlyAPIToken  string `json:"fly_api_token"`
}

func deployHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a deployArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Confirm && a.AuthorizedBy == "" {
			return nil, fmt.Errorf("authorized_by is required when confirm=true — set it to a direct quote or paraphrase of the user's deploy instruction (e.g. \"user said: deploy\"). This prevents accidental deploys from replayed or ambiguous messages.")
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

		// Get current deployed commit — prefer the live /version endpoint
		// over the stored value, since the store entry can be missing after
		// a volume wipe or if a deploy happened outside this tool.
		deployedCommit, err := deps.Store.GetDeployedCommit(ctx)
		if err != nil {
			return nil, err
		}
		if liveCommit := fetchLiveVersion(ctx, deps.Store); liveCommit != "" {
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
		// Use GO_BUILD_PARALLEL=1 to reduce memory usage on the remote builder.
		// The gotd/td dependency is large and OOMs the builder at higher parallelism.
		cmd = exec.Command("fly", "deploy", "--remote-only", "--build-arg", "GO_BUILD_PARALLEL=1", "-a", appName)
		cmd.Dir = checkoutDir
		cmd.Env = append(cmd.Env, "FLY_API_TOKEN="+flyToken, "PATH="+os.Getenv("PATH"), "HOME="+os.Getenv("HOME"))
		deployOut, err := cmd.CombinedOutput()
		if err != nil {
			out := string(deployOut)
			if strings.Contains(out, "signal: killed") {
				return nil, fmt.Errorf("fly deploy failed: remote builder ran out of memory (OOM) compiling Go — try increasing GO_BUILD_PARALLEL or deploying locally with `tclaw deploy`: %w", err)
			}
			return nil, fmt.Errorf("fly deploy failed: %s: %w", out, err)
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
