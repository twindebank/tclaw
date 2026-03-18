package repotools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthenticatedURL(t *testing.T) {
	t.Run("injects token into HTTPS URL", func(t *testing.T) {
		got := authenticatedURL("https://github.com/user/repo", "ghp_abc123")
		require.Equal(t, "https://ghp_abc123@github.com/user/repo", got)
	})

	t.Run("returns original URL when token is empty", func(t *testing.T) {
		got := authenticatedURL("https://github.com/user/repo", "")
		require.Equal(t, "https://github.com/user/repo", got)
	})

	t.Run("only replaces first https:// occurrence", func(t *testing.T) {
		got := authenticatedURL("https://github.com/user/repo?redirect=https://example.com", "tok")
		require.Equal(t, "https://tok@github.com/user/repo?redirect=https://example.com", got)
	})
}

func TestSanitizeGitOutput(t *testing.T) {
	t.Run("redacts token from output", func(t *testing.T) {
		got := sanitizeGitOutput("fatal: could not read from remote 'https://ghp_secret123@github.com/repo'", "ghp_secret123")
		require.Contains(t, got, "[REDACTED]")
		require.NotContains(t, got, "ghp_secret123")
	})

	t.Run("returns output unchanged when token is empty", func(t *testing.T) {
		output := "fatal: repository not found"
		got := sanitizeGitOutput(output, "")
		require.Equal(t, output, got)
	})

	t.Run("redacts multiple occurrences", func(t *testing.T) {
		got := sanitizeGitOutput("token=abc token=abc", "abc")
		require.Equal(t, "token=[REDACTED] token=[REDACTED]", got)
	})
}

func TestShallowCloneOrFetch(t *testing.T) {
	t.Run("first clone creates bare repo with HEAD", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		bareDir := filepath.Join(t.TempDir(), "bare")
		require.NoError(t, os.MkdirAll(bareDir, 0o755))

		require.NoError(t, shallowCloneOrFetch(bareDir, remote, "main", "", 50))

		_, err := os.Stat(filepath.Join(bareDir, "HEAD"))
		require.NoError(t, err)

		sha, err := headCommitSHA(bareDir, "main")
		require.NoError(t, err)
		require.NotEmpty(t, sha)
	})

	t.Run("subsequent fetch advances HEAD", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		bareDir := filepath.Join(t.TempDir(), "bare")
		require.NoError(t, os.MkdirAll(bareDir, 0o755))

		require.NoError(t, shallowCloneOrFetch(bareDir, remote, "main", "", 50))
		sha1, err := headCommitSHA(bareDir, "main")
		require.NoError(t, err)

		addCommitToRemote(t, remote, "main", "second.txt", "second commit")

		require.NoError(t, shallowCloneOrFetch(bareDir, remote, "main", "", 50))
		sha2, err := headCommitSHA(bareDir, "main")
		require.NoError(t, err)
		require.NotEqual(t, sha1, sha2, "HEAD should advance after fetch")
	})

	t.Run("pre-created dir without HEAD still clones", func(t *testing.T) {
		// Reproduces the bug from commit 5356c57: repo_add creates the
		// directory eagerly, so shallowCloneOrFetch must check for HEAD,
		// not just the directory's existence.
		remote := createTestRemote(t, "main")
		bareDir := filepath.Join(t.TempDir(), "bare")
		require.NoError(t, os.MkdirAll(bareDir, 0o755))

		_, err := os.Stat(filepath.Join(bareDir, "HEAD"))
		require.True(t, os.IsNotExist(err), "HEAD should not exist before clone")

		require.NoError(t, shallowCloneOrFetch(bareDir, remote, "main", "", 50))

		sha, err := headCommitSHA(bareDir, "main")
		require.NoError(t, err)
		require.NotEmpty(t, sha)
	})
}

func TestReadOnlyCheckout(t *testing.T) {
	t.Run("creates worktree from scratch", func(t *testing.T) {
		remote, bareDir := clonedBareRepo(t, "main")
		_ = remote
		checkoutDir := filepath.Join(t.TempDir(), "checkout")

		result, err := readOnlyCheckout(bareDir, checkoutDir, "main")
		require.NoError(t, err)
		require.Equal(t, "created", result.Action)
		require.Greater(t, result.FileCount, 0)

		_, err = os.Stat(filepath.Join(checkoutDir, "init.txt"))
		require.NoError(t, err)
	})

	t.Run("updates existing worktree with new commits", func(t *testing.T) {
		remote, bareDir := clonedBareRepo(t, "main")
		checkoutDir := filepath.Join(t.TempDir(), "checkout")

		_, err := readOnlyCheckout(bareDir, checkoutDir, "main")
		require.NoError(t, err)

		addCommitToRemote(t, remote, "main", "new.txt", "new commit")
		require.NoError(t, shallowCloneOrFetch(bareDir, remote, "main", "", 50))

		result, err := readOnlyCheckout(bareDir, checkoutDir, "main")
		require.NoError(t, err)
		require.Equal(t, "updated", result.Action)
		require.Greater(t, result.FileCount, 0)

		_, err = os.Stat(filepath.Join(checkoutDir, "new.txt"))
		require.NoError(t, err)
	})

	t.Run("recreates stale worktree after bare repo re-clone", func(t *testing.T) {
		remote, bareDir := clonedBareRepo(t, "main")
		checkoutDir := filepath.Join(t.TempDir(), "checkout")

		_, err := readOnlyCheckout(bareDir, checkoutDir, "main")
		require.NoError(t, err)

		// Simulate volume wipe + re-clone: replace the bare repo entirely.
		require.NoError(t, os.RemoveAll(bareDir))
		require.NoError(t, os.MkdirAll(bareDir, 0o755))
		require.NoError(t, shallowCloneOrFetch(bareDir, remote, "main", "", 50))

		// checkoutDir still exists but is stale — the bare repo it was
		// linked to is gone.
		result, err := readOnlyCheckout(bareDir, checkoutDir, "main")
		require.NoError(t, err)
		require.Equal(t, "recreated", result.Action)
		require.Greater(t, result.FileCount, 0)

		_, err = os.Stat(filepath.Join(checkoutDir, "init.txt"))
		require.NoError(t, err)
	})

	t.Run("recreates empty pre-created dir from repo_add", func(t *testing.T) {
		// repo_add pre-creates the checkout dir as an empty directory. If
		// repo_sync never ran, the dir exists but isn't a worktree.
		_, bareDir := clonedBareRepo(t, "main")
		checkoutDir := filepath.Join(t.TempDir(), "checkout")
		require.NoError(t, os.MkdirAll(checkoutDir, 0o755))

		result, err := readOnlyCheckout(bareDir, checkoutDir, "main")
		require.NoError(t, err)
		require.Equal(t, "recreated", result.Action)
		require.Greater(t, result.FileCount, 0)

		_, err = os.Stat(filepath.Join(checkoutDir, "init.txt"))
		require.NoError(t, err)
	})
}

func TestCommitLogSince(t *testing.T) {
	remote := createTestRemote(t, "main")
	bareDir := filepath.Join(t.TempDir(), "bare")
	require.NoError(t, os.MkdirAll(bareDir, 0o755))
	require.NoError(t, shallowCloneOrFetch(bareDir, remote, "main", "", 50))

	firstSHA, err := headCommitSHA(bareDir, "main")
	require.NoError(t, err)

	addCommitToRemote(t, remote, "main", "a.txt", "commit A")
	addCommitToRemote(t, remote, "main", "b.txt", "commit B")
	require.NoError(t, shallowCloneOrFetch(bareDir, remote, "main", "", 50))

	t.Run("returns commits since a SHA", func(t *testing.T) {
		logOutput, err := commitLogSince(bareDir, "main", firstSHA, 50)
		require.NoError(t, err)

		lines := strings.Split(logOutput, "\n")
		require.Equal(t, 2, len(lines))
		require.Contains(t, logOutput, "commit A")
		require.Contains(t, logOutput, "commit B")
	})

	t.Run("empty since falls back to recent", func(t *testing.T) {
		logOutput, err := commitLogSince(bareDir, "main", "", 50)
		require.NoError(t, err)
		require.NotEmpty(t, logOutput)
	})

	t.Run("pruned SHA falls back to recent", func(t *testing.T) {
		logOutput, err := commitLogSince(bareDir, "main", "0000000000000000000000000000000000000000", 5)
		require.NoError(t, err)
		require.NotEmpty(t, logOutput, "should fall back to recent commits")
	})
}

func TestCommitLogRecent(t *testing.T) {
	remote := createTestRemote(t, "main")
	bareDir := filepath.Join(t.TempDir(), "bare")
	require.NoError(t, os.MkdirAll(bareDir, 0o755))

	addCommitToRemote(t, remote, "main", "a.txt", "commit A")
	addCommitToRemote(t, remote, "main", "b.txt", "commit B")
	require.NoError(t, shallowCloneOrFetch(bareDir, remote, "main", "", 50))

	logOutput, err := commitLogRecent(bareDir, "main", 2)
	require.NoError(t, err)

	lines := strings.Split(logOutput, "\n")
	require.Equal(t, 2, len(lines))
}

func TestHeadCommitSHA(t *testing.T) {
	_, bareDir := clonedBareRepo(t, "main")

	sha, err := headCommitSHA(bareDir, "main")
	require.NoError(t, err)
	require.Len(t, sha, 40, "should be a full 40-char SHA")
}

// --- helpers ---

// clonedBareRepo creates a test remote and a shallow bare clone of it.
func clonedBareRepo(t *testing.T, branch string) (remoteDir string, bareDir string) {
	t.Helper()
	remote := createTestRemote(t, branch)
	bare := filepath.Join(t.TempDir(), "bare")
	require.NoError(t, os.MkdirAll(bare, 0o755))
	require.NoError(t, shallowCloneOrFetch(bare, remote, branch, "", 50))
	return remote, bare
}

// createTestRemote creates a non-bare git repo with one commit, usable as a
// local "remote" for clone/fetch operations.
func createTestRemote(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, string(out))
	}

	run("init", "--initial-branch", branch)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "init.txt"), []byte("hello"), 0o644))
	run("add", ".")
	run("commit", "-m", "initial commit")

	return dir
}

// addCommitToRemote adds a file and commits it to the test remote.
func addCommitToRemote(t *testing.T, remoteDir string, branch string, filename string, message string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(remoteDir, filename), []byte(message), 0o644))

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", remoteDir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, string(out))
	}

	run("add", filename)
	run("commit", "-m", message)
}
