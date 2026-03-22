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

func TestCloneOrFetch(t *testing.T) {
	t.Run("first clone creates repo with .git", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		repoDir := filepath.Join(t.TempDir(), "clone")
		require.NoError(t, os.MkdirAll(repoDir, 0o755))

		require.NoError(t, cloneOrFetch(repoDir, remote, "main", "", 50))

		_, err := os.Stat(filepath.Join(repoDir, ".git"))
		require.NoError(t, err)

		sha, err := headCommitSHA(repoDir, "main")
		require.NoError(t, err)
		require.NotEmpty(t, sha)

		// Working tree should have the file.
		_, err = os.Stat(filepath.Join(repoDir, "init.txt"))
		require.NoError(t, err)
	})

	t.Run("subsequent fetch updates working tree", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		repoDir := filepath.Join(t.TempDir(), "clone")
		require.NoError(t, os.MkdirAll(repoDir, 0o755))

		require.NoError(t, cloneOrFetch(repoDir, remote, "main", "", 50))
		sha1, err := headCommitSHA(repoDir, "main")
		require.NoError(t, err)

		addCommitToRemote(t, remote, "main", "second.txt", "second commit")

		require.NoError(t, cloneOrFetch(repoDir, remote, "main", "", 50))
		sha2, err := headCommitSHA(repoDir, "main")
		require.NoError(t, err)
		require.NotEqual(t, sha1, sha2, "HEAD should advance after fetch")

		// New file should be in working tree.
		_, err = os.Stat(filepath.Join(repoDir, "second.txt"))
		require.NoError(t, err)
	})

	t.Run("pre-created dir without .git still clones", func(t *testing.T) {
		// repo_add creates the directory eagerly, so cloneOrFetch must
		// check for .git, not just the directory's existence.
		remote := createTestRemote(t, "main")
		repoDir := filepath.Join(t.TempDir(), "clone")
		require.NoError(t, os.MkdirAll(repoDir, 0o755))

		_, err := os.Stat(filepath.Join(repoDir, ".git"))
		require.True(t, os.IsNotExist(err), ".git should not exist before clone")

		require.NoError(t, cloneOrFetch(repoDir, remote, "main", "", 50))

		sha, err := headCommitSHA(repoDir, "main")
		require.NoError(t, err)
		require.NotEmpty(t, sha)
	})

	t.Run("deleted files removed on fetch", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		repoDir := filepath.Join(t.TempDir(), "clone")
		require.NoError(t, os.MkdirAll(repoDir, 0o755))

		addCommitToRemote(t, remote, "main", "ephemeral.txt", "will be deleted")
		require.NoError(t, cloneOrFetch(repoDir, remote, "main", "", 50))

		_, err := os.Stat(filepath.Join(repoDir, "ephemeral.txt"))
		require.NoError(t, err, "ephemeral.txt should exist after clone")

		deleteFileInRemote(t, remote, "main", "ephemeral.txt")
		require.NoError(t, cloneOrFetch(repoDir, remote, "main", "", 50))

		_, err = os.Stat(filepath.Join(repoDir, "ephemeral.txt"))
		require.True(t, os.IsNotExist(err), "ephemeral.txt should be gone after fetch+reset")
	})
}

func TestCommitLogSince(t *testing.T) {
	remote := createTestRemote(t, "main")
	repoDir := filepath.Join(t.TempDir(), "clone")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))
	require.NoError(t, cloneOrFetch(repoDir, remote, "main", "", 50))

	firstSHA, err := headCommitSHA(repoDir, "main")
	require.NoError(t, err)

	addCommitToRemote(t, remote, "main", "a.txt", "commit A")
	addCommitToRemote(t, remote, "main", "b.txt", "commit B")
	require.NoError(t, cloneOrFetch(repoDir, remote, "main", "", 50))

	t.Run("returns commits since a SHA", func(t *testing.T) {
		logOutput, err := commitLogSince(repoDir, "main", firstSHA, 50)
		require.NoError(t, err)

		lines := strings.Split(logOutput, "\n")
		require.Equal(t, 2, len(lines))
		require.Contains(t, logOutput, "commit A")
		require.Contains(t, logOutput, "commit B")
	})

	t.Run("empty since falls back to recent", func(t *testing.T) {
		logOutput, err := commitLogSince(repoDir, "main", "", 50)
		require.NoError(t, err)
		require.NotEmpty(t, logOutput)
	})

	t.Run("pruned SHA falls back to recent", func(t *testing.T) {
		logOutput, err := commitLogSince(repoDir, "main", "0000000000000000000000000000000000000000", 5)
		require.NoError(t, err)
		require.NotEmpty(t, logOutput, "should fall back to recent commits")
	})
}

func TestCommitLogRecent(t *testing.T) {
	remote := createTestRemote(t, "main")
	repoDir := filepath.Join(t.TempDir(), "clone")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))

	addCommitToRemote(t, remote, "main", "a.txt", "commit A")
	addCommitToRemote(t, remote, "main", "b.txt", "commit B")
	require.NoError(t, cloneOrFetch(repoDir, remote, "main", "", 50))

	logOutput, err := commitLogRecent(repoDir, "main", 2)
	require.NoError(t, err)

	lines := strings.Split(logOutput, "\n")
	require.Equal(t, 2, len(lines))
}

func TestHeadCommitSHA(t *testing.T) {
	remote := createTestRemote(t, "main")
	repoDir := filepath.Join(t.TempDir(), "clone")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))
	require.NoError(t, cloneOrFetch(repoDir, remote, "main", "", 50))

	sha, err := headCommitSHA(repoDir, "main")
	require.NoError(t, err)
	require.Len(t, sha, 40, "should be a full 40-char SHA")
}

func TestCountFiles(t *testing.T) {
	t.Run("excludes .git directory", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		repoDir := filepath.Join(t.TempDir(), "clone")
		require.NoError(t, os.MkdirAll(repoDir, 0o755))
		require.NoError(t, cloneOrFetch(repoDir, remote, "main", "", 50))

		count := countFiles(repoDir)
		// Should be 1 (init.txt), not 2 (init.txt + .git).
		require.Equal(t, 1, count)
	})
}

// --- helpers ---

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

// deleteFileInRemote removes a file and commits the deletion to the test remote.
func deleteFileInRemote(t *testing.T, remoteDir string, branch string, filename string) {
	t.Helper()
	require.NoError(t, os.Remove(filepath.Join(remoteDir, filename)))

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
	run("commit", "-m", "delete "+filename)
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
