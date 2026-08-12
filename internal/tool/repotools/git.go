package repotools

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CloneParams describes the clone to create or refresh.
type CloneParams struct {
	// RepoDir is the absolute path of the local clone.
	RepoDir string

	// URL is the token-free HTTPS clone URL.
	URL string

	// Branch is the single branch tracked on the remote.
	Branch string

	// Token authenticates private repos. Empty for public ones.
	Token string

	// Depth caps how much history is fetched (shallow clone).
	Depth int
}

// CloneOrFetch ensures a non-bare clone exists at RepoDir tracking the given
// branch. First call does a shallow single-branch clone, subsequent calls
// fetch with the given depth. After fetching, the working tree is reset to
// match the remote branch tip so the agent always sees the latest files.
//
// Exported so the router can provision config-declared repos on boot with the
// same clone semantics the repo tools use at runtime.
func CloneOrFetch(params CloneParams) error {
	depthArg := fmt.Sprintf("--depth=%d", params.Depth)
	auth := authConfigArgs(params.Token)

	// Check for the .git directory — repo_add creates repoDir eagerly,
	// so a missing .git means we haven't cloned yet.
	dotGit := filepath.Join(params.RepoDir, ".git")
	if _, err := os.Stat(dotGit); os.IsNotExist(err) {
		// If the directory is non-empty (e.g. a stale bare repo from a previous
		// code version, or a partial clone that never completed), remove it so
		// git clone can start fresh.
		entries, err := os.ReadDir(params.RepoDir)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read repo dir: %w", err)
		}
		if len(entries) > 0 {
			slog.Info("removing stale repo dir before re-cloning", "repo_dir", params.RepoDir)
			if err := os.RemoveAll(params.RepoDir); err != nil {
				return fmt.Errorf("remove stale repo dir: %w", err)
			}
		}
		slog.Info("cloning repo", "repo_dir", params.RepoDir, "branch", params.Branch)
		args := append([]string{"-c", "core.hooksPath=/dev/null"}, auth...)
		args = append(args, "clone", depthArg, "--single-branch", "--branch", params.Branch,
			params.URL, params.RepoDir)
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clone: %s: %w", sanitizeGitOutput(string(out), params.Token), err)
		}
		return nil
	}

	// Pin origin to the token-free URL. Besides surviving a config change, this
	// rewrites clones made by older versions that embedded the token in the
	// remote URL — and so left it readable in .git/config inside the sandbox.
	cmd := exec.Command("git", "-C", params.RepoDir, "remote", "set-url", "origin", params.URL)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git set-url: %s: %w", sanitizeGitOutput(string(out), params.Token), err)
	}

	fetchArgs := append([]string{"-C", params.RepoDir}, auth...)
	fetchArgs = append(fetchArgs, "fetch", "origin", params.Branch, depthArg)
	cmd = exec.Command("git", fetchArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %s: %w", sanitizeGitOutput(string(out), params.Token), err)
	}

	// Reset the working tree to match the remote branch tip. This is a
	// read-only monitoring clone so there's nothing to preserve.
	cmd = exec.Command("git", "-C", params.RepoDir, "reset", "--hard", "origin/"+params.Branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset: %s: %w", sanitizeGitOutput(string(out), params.Token), err)
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

// authConfigArgs returns the `git -c` flags that authenticate a fetch, or nil
// for public repos. The token travels as a per-invocation HTTP header rather
// than embedded in the remote URL: the URL would be written to .git/config,
// which lives inside a directory bound into the agent's sandbox and would hand
// the agent a working GitHub PAT. These git commands run in the tclaw process,
// outside the sandbox's PID namespace, so their argv is not readable by the
// agent.
//
// GitHub accepts a PAT as the HTTP Basic password with any username; the
// conventional "x-access-token" is used here.
func authConfigArgs(token string) []string {
	if token == "" {
		return nil
	}
	credentials := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return []string{"-c", "http.extraHeader=Authorization: Basic " + credentials}
}

// sanitizeGitOutput redacts a token from git command output to prevent
// credential leakage in error messages. Both the raw token and its encoded
// Basic-auth form are redacted, since either may be echoed back by git.
func sanitizeGitOutput(output string, token string) string {
	if token == "" {
		return output
	}
	output = strings.ReplaceAll(output, token, "[REDACTED]")
	credentials := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return strings.ReplaceAll(output, credentials, "[REDACTED]")
}
