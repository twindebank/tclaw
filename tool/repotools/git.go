package repotools

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// shallowCloneOrFetch ensures a bare repo exists at repoDir. First call does a
// shallow single-branch clone, subsequent calls fetch with the given depth.
func shallowCloneOrFetch(repoDir string, repoURL string, branch string, token string, depth int) error {
	authURL := authenticatedURL(repoURL, token)
	depthArg := fmt.Sprintf("--depth=%d", depth)

	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		cmd := exec.Command("git", "-c", "core.hooksPath=/dev/null",
			"clone", "--bare", depthArg, "--single-branch", "--branch", branch,
			authURL, repoDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clone: %s: %w", sanitizeGitOutput(string(out), token), err)
		}

		// Configure fetch refspec so origin/<branch> refs are available.
		cmd = exec.Command("git", "-C", repoDir, "config", "remote.origin.fetch",
			fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch))
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git config fetch refspec: %s: %w", sanitizeGitOutput(string(out), token), err)
		}

		cmd = exec.Command("git", "-C", repoDir, "fetch", "origin")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git initial fetch: %s: %w", sanitizeGitOutput(string(out), token), err)
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
	return nil
}

// readOnlyCheckout creates or updates a detached worktree at checkoutDir
// pointing to the HEAD of the given branch on the remote.
func readOnlyCheckout(repoDir string, checkoutDir string, branch string) error {
	ref := "origin/" + branch

	if _, err := os.Stat(checkoutDir); os.IsNotExist(err) {
		// First checkout — create the worktree.
		cmd := exec.Command("git", "-c", "core.hooksPath=/dev/null", "-C", repoDir,
			"worktree", "add", "--detach", checkoutDir, ref)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git worktree add: %s: %w", string(out), err)
		}
		return nil
	}

	// Worktree exists — update to latest.
	cmd := exec.Command("git", "-C", checkoutDir, "checkout", "--detach", ref)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout: %s: %w", string(out), err)
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

// worktreeRemove removes a worktree directory.
func worktreeRemove(repoDir string, worktreeDir string) error {
	cmd := exec.Command("git", "-C", repoDir, "worktree", "remove", "--force", worktreeDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s: %w", string(out), err)
	}
	return nil
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
