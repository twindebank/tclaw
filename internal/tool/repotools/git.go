package repotools

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cloneOrFetch ensures a non-bare clone exists at repoDir tracking the given
// branch. First call does a shallow single-branch clone, subsequent calls
// fetch with the given depth. After fetching, the working tree is reset to
// match the remote branch tip so the agent always sees the latest files.
func cloneOrFetch(repoDir string, repoURL string, branch string, token string, depth int) error {
	authURL := authenticatedURL(repoURL, token)
	depthArg := fmt.Sprintf("--depth=%d", depth)

	// Check for the .git directory — repo_add creates repoDir eagerly,
	// so a missing .git means we haven't cloned yet.
	dotGit := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(dotGit); os.IsNotExist(err) {
		// If the directory is non-empty (e.g. a stale bare repo from a previous
		// code version, or a partial clone that never completed), remove it so
		// git clone can start fresh.
		entries, err := os.ReadDir(repoDir)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read repo dir: %w", err)
		}
		if len(entries) > 0 {
			slog.Info("removing stale repo dir before re-cloning", "repo_dir", repoDir)
			if err := os.RemoveAll(repoDir); err != nil {
				return fmt.Errorf("remove stale repo dir: %w", err)
			}
		}
		slog.Info("cloning repo", "repo_dir", repoDir, "branch", branch)
		cmd := exec.Command("git", "-c", "core.hooksPath=/dev/null",
			"clone", depthArg, "--single-branch", "--branch", branch,
			authURL, repoDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clone: %s: %w", sanitizeGitOutput(string(out), token), err)
		}
		return nil
	}

	// Update the remote URL in case the token changed.
	cmd := exec.Command("git", "-C", repoDir, "remote", "set-url", "origin", authURL)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git set-url: %s: %w", sanitizeGitOutput(string(out), token), err)
	}

	cmd = exec.Command("git", "-C", repoDir, "fetch", "origin", branch, depthArg)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %s: %w", sanitizeGitOutput(string(out), token), err)
	}

	// Reset the working tree to match the remote branch tip. This is a
	// read-only monitoring clone so there's nothing to preserve.
	cmd = exec.Command("git", "-C", repoDir, "reset", "--hard", "origin/"+branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset: %s: %w", sanitizeGitOutput(string(out), token), err)
	}

	return nil
}

// headCommitSHA returns the full SHA of the branch tip on the remote.
func headCommitSHA(repoDir string, branch string) (string, error) {
	cmd := exec.Command("git", "-C", repoDir, "rev-parse", "origin/"+branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// commitLogSince returns commits on the branch since the given SHA.
// If sinceCommit is empty, returns the last `limit` commits instead.
func commitLogSince(repoDir string, branch string, sinceCommit string, limit int) (string, error) {
	if sinceCommit == "" {
		return commitLogRecent(repoDir, branch, limit)
	}
	cmd := exec.Command("git", "-C", repoDir, "log", "--oneline",
		fmt.Sprintf("%s..origin/%s", sinceCommit, branch))
	out, err := cmd.CombinedOutput()
	if err != nil {
		// The since-commit may have been pruned from shallow history.
		// Fall back to recent commits.
		slog.Debug("commit log since failed, falling back to recent", "err", err)
		return commitLogRecent(repoDir, branch, limit)
	}
	return strings.TrimSpace(string(out)), nil
}

// commitLogRecent returns the last N commits on the branch.
func commitLogRecent(repoDir string, branch string, count int) (string, error) {
	cmd := exec.Command("git", "-C", repoDir, "log", "--oneline",
		fmt.Sprintf("-n%d", count), "origin/"+branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git log: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// commitLogDetailed returns commits with optional diffstat.
func commitLogDetailed(repoDir string, branch string, count int, since string, includeDiff bool) (string, error) {
	args := []string{"-C", repoDir, "log", fmt.Sprintf("-n%d", count)}
	if since != "" {
		args = append(args, "--since="+since)
	}
	if includeDiff {
		args = append(args, "--stat")
	}
	args = append(args, "origin/"+branch)

	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git log: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// countFiles returns the number of top-level entries in a directory, or 0 if
// the directory doesn't exist or can't be read.
func countFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	// Subtract 1 for the .git directory.
	count := 0
	for _, e := range entries {
		if e.Name() != ".git" {
			count++
		}
	}
	return count
}

// authenticatedURL injects a GitHub token into an HTTPS URL for auth.
func authenticatedURL(repoURL string, token string) string {
	if token == "" {
		return repoURL
	}
	return strings.Replace(repoURL, "https://", "https://"+token+"@", 1)
}

// sanitizeGitOutput redacts a token from git command output to prevent
// credential leakage in error messages.
func sanitizeGitOutput(output string, token string) string {
	if token == "" {
		return output
	}
	return strings.ReplaceAll(output, token, "[REDACTED]")
}
