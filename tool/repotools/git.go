package repotools

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// shallowCloneOrFetch ensures a bare repo exists at repoDir. First call does a
// shallow single-branch clone, subsequent calls fetch with the given depth.
func shallowCloneOrFetch(repoDir string, repoURL string, branch string, token string, depth int) error {
	authURL := authenticatedURL(repoURL, token)
	depthArg := fmt.Sprintf("--depth=%d", depth)

	// Check for the HEAD file rather than the directory — repo_add creates the
	// directory eagerly, so a missing HEAD means we haven't cloned yet.
	if _, err := os.Stat(filepath.Join(repoDir, "HEAD")); os.IsNotExist(err) {
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

// checkoutResult describes what readOnlyCheckout did and the state it left
// the checkout dir in. Returned to the caller so the agent-facing response
// can include diagnostic detail without a separate ls.
type checkoutResult struct {
	// Action describes what happened: "created", "updated", or "recreated".
	Action string

	// FileCount is the number of top-level entries in the checkout dir after
	// the operation. Zero usually means something went wrong.
	FileCount int
}

// readOnlyCheckout creates or updates a detached worktree at checkoutDir
// pointing to the HEAD of the given branch on the remote.
func readOnlyCheckout(repoDir string, checkoutDir string, branch string) (checkoutResult, error) {
	ref := "origin/" + branch

	if _, err := os.Stat(checkoutDir); os.IsNotExist(err) {
		slog.Info("checkout dir does not exist, creating worktree", "checkout_dir", checkoutDir, "repo_dir", repoDir, "ref", ref)
		fileCount, addErr := worktreeAdd(repoDir, checkoutDir, ref)
		if addErr != nil {
			return checkoutResult{}, addErr
		}
		return checkoutResult{Action: "created", FileCount: fileCount}, nil
	}

	// Diagnose what kind of directory we're dealing with — a real worktree
	// has a .git file; an empty pre-created dir or broken worktree won't.
	gitPath := filepath.Join(checkoutDir, ".git")
	gitInfo, gitErr := os.Stat(gitPath)
	isWorktree := gitErr == nil && !gitInfo.IsDir()
	slog.Info("checkout dir exists, attempting update",
		"checkout_dir", checkoutDir,
		"ref", ref,
		"has_dotgit_file", isWorktree,
	)

	if !isWorktree {
		// Not a real worktree (e.g. empty dir from repo_add, or .git is a
		// directory instead of a file). Remove and create fresh.
		slog.Warn("checkout dir is not a valid worktree, recreating",
			"checkout_dir", checkoutDir,
			"has_dotgit", gitErr == nil,
			"dotgit_is_dir", gitErr == nil && gitInfo.IsDir(),
		)
		return recreateWorktree(repoDir, checkoutDir, ref)
	}

	// Worktree exists — update to latest. If this fails, the worktree is
	// stale (e.g. bare repo was re-cloned after a volume wipe). Remove
	// the stale directory and recreate from scratch.
	cmd := exec.Command("git", "-C", checkoutDir, "checkout", "--detach", ref)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Warn("git checkout failed on existing worktree, recreating",
			"err", err,
			"output", strings.TrimSpace(string(out)),
			"checkout_dir", checkoutDir,
		)
		return recreateWorktree(repoDir, checkoutDir, ref)
	}

	fileCount := countFiles(checkoutDir)
	slog.Info("checkout updated successfully", "checkout_dir", checkoutDir, "ref", ref, "file_count", fileCount)
	return checkoutResult{Action: "updated", FileCount: fileCount}, nil
}

// recreateWorktree removes a broken/stale checkout dir and creates a fresh
// worktree in its place.
func recreateWorktree(repoDir string, checkoutDir string, ref string) (checkoutResult, error) {
	if err := os.RemoveAll(checkoutDir); err != nil {
		return checkoutResult{}, fmt.Errorf("remove stale worktree: %w", err)
	}
	// Prune the bare repo's worktree list so git doesn't reject the
	// re-add with "already registered".
	prune := exec.Command("git", "-C", repoDir, "worktree", "prune")
	if out, err := prune.CombinedOutput(); err != nil {
		return checkoutResult{}, fmt.Errorf("git worktree prune: %s: %w", string(out), err)
	}
	fileCount, err := worktreeAdd(repoDir, checkoutDir, ref)
	if err != nil {
		return checkoutResult{}, err
	}
	return checkoutResult{Action: "recreated", FileCount: fileCount}, nil
}

// worktreeAdd creates a detached worktree and returns the file count, or an
// error if the worktree was not created or is empty.
func worktreeAdd(repoDir string, checkoutDir string, ref string) (int, error) {
	slog.Info("creating worktree", "checkout_dir", checkoutDir, "ref", ref, "repo_dir", repoDir)
	cmd := exec.Command("git", "-c", "core.hooksPath=/dev/null", "-C", repoDir,
		"worktree", "add", "--detach", checkoutDir, ref)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("git worktree add: %s: %w", string(out), err)
	}

	fileCount := countFiles(checkoutDir)
	if fileCount == 0 {
		// git exited 0 but no files appeared — something is wrong.
		return 0, fmt.Errorf("git worktree add succeeded (exit 0) but checkout dir is empty: %s", checkoutDir)
	}
	slog.Info("worktree created successfully", "checkout_dir", checkoutDir, "file_count", fileCount)
	return fileCount, nil
}

// countFiles returns the number of top-level entries in a directory, or 0 if
// the directory doesn't exist or can't be read.
func countFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	return len(entries)
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
