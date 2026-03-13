package devtools

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// cloneOrFetch ensures a bare repo exists at repoDir. If the directory doesn't
// exist, it clones. Otherwise it fetches. Returns an error if git operations fail.
func cloneOrFetch(repoDir string, repoURL string, token string) error {
	authURL := authenticatedURL(repoURL, token)

	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		cmd := exec.Command("git", "-c", "core.hooksPath=/dev/null", "clone", "--bare", authURL, repoDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clone: %s: %w", sanitizeGitOutput(string(out), token), err)
		}
		// Bare clones don't set up remote tracking refs (origin/*) by default.
		// Configure the fetch refspec so that `git fetch origin` populates
		// refs/remotes/origin/*, which worktreeAdd relies on.
		cmd = exec.Command("git", "-C", repoDir, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git config fetch refspec: %s: %w", sanitizeGitOutput(string(out), token), err)
		}
		cmd = exec.Command("git", "-C", repoDir, "fetch", "origin")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git initial fetch: %s: %w", sanitizeGitOutput(string(out), token), err)
		}
		return nil
	}

	cmd := exec.Command("git", "-C", repoDir, "remote", "set-url", "origin", authURL)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git set-url: %s: %w", sanitizeGitOutput(string(out), token), err)
	}

	// Ensure the fetch refspec is configured (may be missing on bare repos
	// created before this fix was added).
	cmd = exec.Command("git", "-C", repoDir, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git config fetch refspec: %s: %w", sanitizeGitOutput(string(out), token), err)
	}

	cmd = exec.Command("git", "-C", repoDir, "fetch", "origin")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %s: %w", sanitizeGitOutput(string(out), token), err)
	}
	return nil
}

// worktreeAdd creates a new worktree for a branch. If branch exists on the
// remote, it checks it out. Otherwise it creates a new branch from origin/main.
func worktreeAdd(repoDir string, worktreeDir string, branch string) error {
	// Check if the branch exists on the remote.
	cmd := exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "origin/"+branch)
	if err := cmd.Run(); err == nil {
		// Branch exists on remote — check it out.
		cmd = exec.Command("git", "-c", "core.hooksPath=/dev/null", "-C", repoDir, "worktree", "add", worktreeDir, "origin/"+branch)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git worktree add (existing branch): %s: %w", string(out), err)
		}
		// Create a local tracking branch.
		cmd = exec.Command("git", "-c", "core.hooksPath=/dev/null", "-C", worktreeDir, "checkout", "-B", branch, "origin/"+branch)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git checkout tracking branch: %s: %w", string(out), err)
		}
		return nil
	}

	// New branch from origin/main.
	cmd = exec.Command("git", "-c", "core.hooksPath=/dev/null", "-C", repoDir, "worktree", "add", "-b", branch, worktreeDir, "origin/main")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add (new branch): %s: %w", string(out), err)
	}
	return nil
}

// worktreeRemove removes a worktree and its local branch.
func worktreeRemove(repoDir string, worktreeDir string, branch string) error {
	cmd := exec.Command("git", "-C", repoDir, "worktree", "remove", "--force", worktreeDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s: %w", string(out), err)
	}

	cmd = exec.Command("git", "-C", repoDir, "branch", "-D", branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Non-fatal: the branch may have already been cleaned up by worktree removal.
		slog.Debug("failed to delete local branch (may already be cleaned up)", "branch", branch, "output", string(out), "err", err)
	}
	return nil
}

// gitStatus returns the porcelain status output for a worktree.
func gitStatus(worktreeDir string) (string, error) {
	cmd := exec.Command("git", "-C", worktreeDir, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git status: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitLog returns the one-line commit log between origin/main and HEAD.
func gitLog(worktreeDir string) (string, error) {
	cmd := exec.Command("git", "-C", worktreeDir, "log", "--oneline", "origin/main..HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git log: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitDiffStat returns the diff stat between origin/main and HEAD.
func gitDiffStat(worktreeDir string) (string, error) {
	cmd := exec.Command("git", "-C", worktreeDir, "diff", "--stat", "origin/main..HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff stat: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitAddAndCommit stages all changes and commits with the given message.
// Returns true if a commit was created, false if there was nothing to commit.
func gitAddAndCommit(worktreeDir string, message string) (bool, error) {
	cmd := exec.Command("git", "-c", "core.hooksPath=/dev/null", "-C", worktreeDir, "add", "-A")
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("git add: %s: %w", string(out), err)
	}

	// Check if there's anything to commit.
	status, err := gitStatus(worktreeDir)
	if err != nil {
		return false, err
	}
	if status == "" {
		return false, nil
	}

	cmd = exec.Command("git", "-c", "core.hooksPath=/dev/null", "-C", worktreeDir, "commit", "-m", message)
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("git commit: %s: %w", string(out), err)
	}
	return true, nil
}

// gitPush pushes the branch to origin.
func gitPush(worktreeDir string, branch string, token string, repoURL string) error {
	authURL := authenticatedURL(repoURL, token)
	cmd := exec.Command("git", "-c", "core.hooksPath=/dev/null", "-C", worktreeDir, "push", authURL, branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push: %s: %w", sanitizeGitOutput(string(out), token), err)
	}
	return nil
}

// gitHeadCommit returns the short hash and subject of HEAD.
func gitHeadCommit(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "log", "-1", "--format=%h %s")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git log HEAD: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitLogRange returns the one-line commit log between two refs.
func gitLogRange(repoDir string, from string, to string) (string, error) {
	cmd := exec.Command("git", "-C", repoDir, "log", "--oneline", from+".."+to)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git log range: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitDiffStatRange returns the diff stat between two refs.
func gitDiffStatRange(repoDir string, from string, to string) (string, error) {
	cmd := exec.Command("git", "-C", repoDir, "diff", "--stat", from+".."+to)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff stat range: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// sanitizeGitOutput redacts a token from git command output to prevent
// credential leakage in error messages returned to callers.
func sanitizeGitOutput(output string, token string) string {
	if token == "" {
		return output
	}
	return strings.ReplaceAll(output, token, "[REDACTED]")
}

// authenticatedURL injects a GitHub token into an HTTPS URL for push/clone auth.
func authenticatedURL(repoURL string, token string) string {
	if token == "" {
		return repoURL
	}
	// https://github.com/user/repo → https://<token>@github.com/user/repo
	return strings.Replace(repoURL, "https://", "https://"+token+"@", 1)
}

// ghEnv returns a minimal environment for gh CLI commands with the GitHub token
// set via GH_TOKEN. Builds an allowlist instead of inheriting the full parent
// environment, which would leak sensitive vars like TCLAW_SECRET_KEY.
func ghEnv(token string) []string {
	var env []string
	for _, prefix := range []string{"PATH", "HOME", "USER", "LANG", "LC_", "TERM", "TMPDIR", "SSL_CERT", "GIT_"} {
		for _, kv := range os.Environ() {
			key, _, _ := strings.Cut(kv, "=")
			if key == prefix || strings.HasPrefix(key, prefix) {
				env = append(env, kv)
			}
		}
	}
	if token != "" {
		env = append(env, "GH_TOKEN="+token)
	}
	return env
}

// ghPRCreate creates a GitHub PR using the gh CLI and returns the PR URL.
func ghPRCreate(worktreeDir string, branch string, title string, body string, token string) (string, error) {
	cmd := exec.Command("gh", "pr", "create",
		"--title", title,
		"--body", body,
		"--head", branch,
		"--base", "main",
	)
	cmd.Dir = worktreeDir
	cmd.Env = ghEnv(token)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr create: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ghPRFind checks if a PR already exists for a branch. Returns the URL if found.
func ghPRFind(worktreeDir string, branch string, token string) (string, error) {
	cmd := exec.Command("gh", "pr", "list", "--head", branch, "--json", "url", "--jq", ".[0].url")
	cmd.Dir = worktreeDir
	cmd.Env = ghEnv(token)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr list: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}
