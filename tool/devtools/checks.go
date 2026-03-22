package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"tclaw/libraries/credentialerror"

	"tclaw/mcp"
)

func devPRChecksDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "dev_pr_checks",
		Description: "Show CI check results for a pull request. Returns pass/fail status for each check and a direct link to failing runs. Use this to verify CI passes before merging, or to diagnose failures to fix.",
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

		cmd := exec.Command("gh", "pr", "checks", fmt.Sprintf("%d", a.PR), "--watch=false")
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
			"passing": passing,
			"output":  raw,
		}
		return json.Marshal(result)
	}
}
