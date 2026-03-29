package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"regexp"

	"strings"
	"tclaw/libraries/credentialerror"
	"time"
	"unicode"

	"tclaw/dev"
	"tclaw/mcp"
)

func devStartDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "dev_start",
		Description: "Start a dev session: clones/fetches the repo, creates a git worktree on a new branch (or checks out an existing one for PR iteration). Returns the worktree path where you can make changes using Bash/Read/Edit/Write. Multiple sessions can be active concurrently. To iterate on an open PR, pass branch=<branch-name> — the branch name is returned by dev_end and shown in PR URLs.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"description": {
					"type": "string",
					"description": "Short description of the work (used to generate branch name). Always required."
				},
				"branch": {
					"type": "string",
					"description": "Existing branch name to check out (e.g. to iterate on an open PR). When provided, the existing branch is checked out instead of creating a new one. Get the branch name from the dev_end output or the PR. Do NOT pass 'session' — this is the correct parameter for PR iteration."
				},
				"repo_url": {
					"type": "string",
					"description": "GitHub repo URL (e.g. https://github.com/user/repo). Only needed on first use — persisted for subsequent calls."
				},
				"github_token": {
					"type": "string",
					"description": "GitHub Personal Access Token for push/PR access. Only needed on first use — stored encrypted for subsequent calls."
				}
			},
			"required": ["description"]
		}`),
	}
}

type devStartArgs struct {
	Description string `json:"description"`
	Branch      string `json:"branch"`
	RepoURL     string `json:"repo_url"`
	GitHubToken string `json:"github_token"`
}

func devStartHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a devStartArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Description == "" {
			return nil, fmt.Errorf("description is required")
		}

		// Check disk space before creating a worktree — the vendor directory
		// alone is ~500MB and running out mid-checkout leaves broken state.
		if err := checkDiskSpace(filepath.Dir(deps.UserDir)); err != nil {
			return nil, err
		}

		// Resolve and persist repo URL.
		repoURL, err := deps.Store.GetRepoURL(ctx)
		if err != nil {
			return nil, err
		}
		if a.RepoURL != "" {
			repoURL = a.RepoURL
			if err := deps.Store.SetRepoURL(ctx, repoURL); err != nil {
				return nil, fmt.Errorf("persist repo url: %w", err)
			}
		}
		if repoURL == "" {
			return nil, fmt.Errorf("no repo URL configured — provide repo_url parameter (e.g. https://github.com/user/repo)")
		}

		// Resolve and persist GitHub token.
		token, err := deps.SecretStore.Get(ctx, githubTokenKey)
		if err != nil {
			return nil, fmt.Errorf("read github token: %w", err)
		}
		if a.GitHubToken != "" {
			token = a.GitHubToken
			if err := deps.SecretStore.Set(ctx, githubTokenKey, token); err != nil {
				return nil, fmt.Errorf("persist github token: %w", err)
			}
		}
		if token == "" {
			return nil, credentialerror.New(
				"GitHub Configuration",
				"A Personal Access Token with repo scope is needed for push and PR access",
				credentialerror.Field{Key: githubTokenKey, Label: "GitHub Personal Access Token", Description: "Create at github.com/settings/tokens with 'repo' scope"},
			)
		}

		// Determine branch name.
		branch := a.Branch
		if branch == "" {
			branch = generateBranchName(a.Description)
		}

		// Validate branch name to prevent path traversal (e.g. "../../../etc").
		if strings.Contains(branch, "..") || strings.ContainsAny(branch, "/\\") {
			return nil, fmt.Errorf("invalid branch name %q — must not contain path separators or '..'", branch)
		}

		// Check if a session already exists for this branch.
		existing, err := deps.Store.GetSession(ctx, branch)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, fmt.Errorf("session already exists for branch %q — use dev_status to check it or dev_cancel to remove it", branch)
		}

		// Clone or fetch the bare repo.
		repoDir := filepath.Join(deps.UserDir, "repo")
		if err := cloneOrFetch(repoDir, repoURL, token); err != nil {
			return nil, fmt.Errorf("clone/fetch: %w", err)
		}

		// Create worktree.
		worktreeDir := filepath.Join(deps.UserDir, "worktrees", branch)
		if err := worktreeAdd(repoDir, worktreeDir, branch); err != nil {
			return nil, fmt.Errorf("worktree: %w", err)
		}

		// Configure git user in the worktree so commits work.
		if err := configureGitUser(worktreeDir); err != nil {
			slog.Warn("failed to configure git user in worktree", "worktree", worktreeDir, "err", err)
		}

		// Save session.
		sess := dev.Session{
			Branch:      branch,
			WorktreeDir: worktreeDir,
			RepoDir:     repoDir,
			Status:      dev.SessionActive,
			CreatedAt:   time.Now(),
		}
		if err := deps.Store.PutSession(ctx, sess); err != nil {
			return nil, fmt.Errorf("save session: %w", err)
		}

		result := map[string]any{
			"branch":       branch,
			"worktree_dir": worktreeDir,
			"message":      fmt.Sprintf("Dev session started on branch %q. Make changes in %s using Bash/Read/Edit/Write. When done, use dev_end to commit, push, and open a PR.", branch, worktreeDir),
		}
		return json.Marshal(result)
	}
}

var nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)

// generateBranchName creates a date-prefixed slugified branch name.
func generateBranchName(description string) string {
	slug := strings.ToLower(description)
	slug = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' {
			return r
		}
		return -1
	}, slug)
	slug = nonAlphanumeric.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")

	// Cap length to keep branch names reasonable.
	if len(slug) > 60 {
		slug = slug[:60]
		slug = strings.TrimRight(slug, "-")
	}

	return time.Now().Format("2006-01-02") + "-" + slug
}

// configureGitUser sets a default git user for the worktree so commits don't fail.
// Returns an error if either config command fails — the caller can surface this
// so the agent knows git config is incomplete.
func configureGitUser(worktreeDir string) error {
	if out, err := exec.Command("git", "-C", worktreeDir, "config", "user.email", "tclaw@localhost").CombinedOutput(); err != nil {
		return fmt.Errorf("configure git user.email: %w (output: %s)", err, string(out))
	}
	if out, err := exec.Command("git", "-C", worktreeDir, "config", "user.name", "tclaw").CombinedOutput(); err != nil {
		return fmt.Errorf("configure git user.name: %w (output: %s)", err, string(out))
	}
	return nil
}
