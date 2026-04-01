package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"tclaw/internal/libraries/credentialerror"

	"tclaw/internal/mcp"
)

const ToolPRChecks = "dev_pr_checks"

func devPRChecksDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolPRChecks,
		Description: "Show CI check results and current state (open/merged/closed) for a pull request. Always check this before suggesting a merge — if state is \"merged\" the PR is already done.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pr": {
					"type": "integer",
					"description": "Pull request number."
				}
			},
			"required": ["pr"]
		}`),
	}
}

type devPRChecksArgs struct {
	PR int `json:"pr"`
}

func devPRChecksHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a devPRChecksArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.PR <= 0 {
			return nil, fmt.Errorf("pr must be a positive integer")
		}

		token, err := deps.SecretStore.Get(ctx, githubTokenKey)
		if err != nil {
			return nil, fmt.Errorf("read github token: %w", err)
		}
		if token == "" {
			return nil, credentialerror.New(
				"GitHub Configuration",
				"A Personal Access Token with repo scope is needed for PR checks",
				credentialerror.Field{Key: githubTokenKey, Label: "GitHub Personal Access Token", Description: "Create at github.com/settings/tokens with 'repo' scope"},
			)
		}

		// Run gh pr checks in the bare repo directory so gh can infer the repo.
		// Falls back to the first available worktree if the bare repo doesn't exist yet.
		repoDir := filepath.Join(deps.UserDir, "repo")
		prStr := fmt.Sprintf("%d", a.PR)

		// Fetch PR state (open/merged/closed) separately — gh pr checks doesn't include it.
		stateCmd := exec.Command("gh", "pr", "view", prStr, "--json", "state", "--jq", ".state")
		stateCmd.Dir = repoDir
		stateCmd.Env = ghEnv(token)
		stateOut, stateErr := stateCmd.CombinedOutput()
		state := strings.ToLower(strings.TrimSpace(string(stateOut)))
		if stateErr != nil || state == "" {
			state = "unknown"
		}

		cmd := exec.Command("gh", "pr", "checks", prStr, "--watch=false")
		cmd.Dir = repoDir
		cmd.Env = ghEnv(token)
		out, err := cmd.CombinedOutput()
		raw := strings.TrimSpace(string(out))

		// gh pr checks exits non-zero when checks are failing — that's expected
		// and still gives useful output. Only treat it as a hard error if there's
		// no output at all (e.g. network failure, bad PR number).
		if err != nil && raw == "" {
			return nil, fmt.Errorf("gh pr checks: %w", err)
		}

		// Determine overall pass/fail from output presence of failure markers.
		passing := !strings.Contains(raw, "fail") && !strings.Contains(raw, "✗")

		result := map[string]any{
			"pr":      a.PR,
			"state":   state, // "open", "merged", or "closed"
			"passing": passing,
			"output":  raw,
		}
		return json.Marshal(result)
	}
}
