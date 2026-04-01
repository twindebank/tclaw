package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"tclaw/internal/libraries/credentialerror"

	"tclaw/internal/mcp"
)

// mainSnapshotBranch is the fixed worktree directory name used for the
// read-only main snapshot. Not a real branch — HEAD is detached.
const mainSnapshotDir = "main-snapshot"

const ToolBrowse = "dev_browse"

func devBrowseDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolBrowse,
		Description: "Fetch latest main and return a READ-ONLY path to browse the source code. " +
			"Use this to read code, understand the codebase, or check current state WITHOUT starting a dev session. " +
			"⚠️ READ-ONLY: do NOT write, edit, or commit in this directory. Use dev_start for any changes.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func devBrowseHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		repoURL, err := deps.Store.GetRepoURL(ctx)
		if err != nil {
			return nil, err
		}
		if repoURL == "" {
			return nil, fmt.Errorf("no repo URL configured — run dev_start once to set it up")
		}

		token, err := deps.SecretStore.Get(ctx, githubTokenKey)
		if err != nil {
			return nil, fmt.Errorf("read github token: %w", err)
		}
		if token == "" {
			return nil, credentialerror.New(
				"GitHub Configuration",
				"A Personal Access Token with repo scope is needed to browse the repo",
				credentialerror.Field{Key: githubTokenKey, Label: "GitHub Personal Access Token", Description: "Create at github.com/settings/tokens with 'repo' scope"},
			)
		}

		repoDir := filepath.Join(deps.UserDir, "repo")
		if err := cloneOrFetch(repoDir, repoURL, token); err != nil {
			return nil, fmt.Errorf("fetch: %w", err)
		}

		snapshotDir := filepath.Join(deps.UserDir, "worktrees", mainSnapshotDir)

		if _, err := os.Stat(snapshotDir); os.IsNotExist(err) {
			// First call: create a detached-HEAD worktree pointing at origin/main.
			// Detached HEAD avoids creating a local branch that could conflict
			// with real dev sessions.
			cmd := exec.Command("git", "-c", "core.hooksPath=/dev/null", "-C", repoDir,
				"worktree", "add", "--detach", snapshotDir, "origin/main")
			if out, err := cmd.CombinedOutput(); err != nil {
				return nil, fmt.Errorf("git worktree add: %s: %w", string(out), err)
			}
		} else {
			// Subsequent calls: reset to latest origin/main.
			cmd := exec.Command("git", "-C", snapshotDir, "reset", "--hard", "origin/main")
			if out, err := cmd.CombinedOutput(); err != nil {
				return nil, fmt.Errorf("git reset: %s: %w", string(out), err)
			}
		}

		commit, err := gitHeadCommit(snapshotDir)
		if err != nil {
			return nil, err
		}

		result := map[string]any{
			"path":   snapshotDir,
			"commit": commit,
			"warning": "⚠️ READ-ONLY snapshot of main. " +
				"Do NOT write, edit, or commit here. Use dev_start to make changes.",
		}
		return json.Marshal(result)
	}
}
