package repotools

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthConfigArgs(t *testing.T) {
	t.Run("carries the token as a Basic auth header", func(t *testing.T) {
		got := authConfigArgs("ghp_abc123")
		require.Equal(t, []string{
			"-c",
			"http.extraHeader=Authorization: Basic " +
				base64.StdEncoding.EncodeToString([]byte("x-access-token:ghp_abc123")),
		}, got)
	})

	t.Run("returns no args when token is empty", func(t *testing.T) {
		require.Nil(t, authConfigArgs(""))
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

		require.NoError(t, cloneMain(repoDir, remote))

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

		require.NoError(t, cloneMain(repoDir, remote))
		sha1, err := headCommitSHA(repoDir, "main")
		require.NoError(t, err)

		addCommitToRemote(t, remote, "main", "second.txt", "second commit")

		require.NoError(t, cloneMain(repoDir, remote))
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

		require.NoError(t, cloneMain(repoDir, remote))

		sha, err := headCommitSHA(repoDir, "main")
		require.NoError(t, err)
		require.NotEmpty(t, sha)
	})

	t.Run("stale non-empty dir without .git is removed and re-cloned", func(t *testing.T) {
		// Old code stored bare git repos at the repo_dir path. When the code
		// switched to regular clones, existing bare repos caused git clone to
		// fail with "already exists and is not an empty directory".
		remote := createTestRemote(t, "main")
		repoDir := filepath.Join(t.TempDir(), "clone")
		require.NoError(t, os.MkdirAll(repoDir, 0o755))
		// Simulate a stale file left over from a previous code version.
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))

		require.NoError(t, cloneMain(repoDir, remote))

		_, err := os.Stat(filepath.Join(repoDir, ".git"))
		require.NoError(t, err, ".git should exist after re-clone")
	})

	t.Run("token never lands in .git/config", func(t *testing.T) {
		// The clone directory is bound into the agent's sandbox, so a token in
		// the remote URL would hand the agent a working GitHub PAT.
		remote := createTestRemote(t, "main")
		repoDir := filepath.Join(t.TempDir(), "clone")
		require.NoError(t, os.MkdirAll(repoDir, 0o755))

		require.NoError(t, cloneWithToken(repoDir, remote, "ghp_secret123"))
		require.NotContains(t, gitConfigContents(t, repoDir), "ghp_secret123")

		// And again on the fetch path, which rewrites the remote URL.
		require.NoError(t, cloneWithToken(repoDir, remote, "ghp_secret123"))
		require.NotContains(t, gitConfigContents(t, repoDir), "ghp_secret123")
	})

	t.Run("rewrites a token-bearing remote from an older clone", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		repoDir := filepath.Join(t.TempDir(), "clone")
		require.NoError(t, os.MkdirAll(repoDir, 0o755))
		require.NoError(t, cloneMain(repoDir, remote))

		// Simulate the pre-fix layout: token embedded in origin.
		setRemoteURL(t, repoDir, "https://ghp_stale@github.com/user/repo")
		require.Contains(t, gitConfigContents(t, repoDir), "ghp_stale")

		require.NoError(t, cloneWithToken(repoDir, remote, "ghp_secret123"))
		config := gitConfigContents(t, repoDir)
		require.NotContains(t, config, "ghp_stale")
		require.NotContains(t, config, "ghp_secret123")
		require.Contains(t, config, remote)
	})

	t.Run("keeps local work when not resetting to the remote", func(t *testing.T) {
		// A repo the agent can push from must survive a sync: resetting would
		// discard the branch it is part-way through.
		remote := createTestRemote(t, "main")
		repoDir := filepath.Join(t.TempDir(), "clone")
		require.NoError(t, os.MkdirAll(repoDir, 0o755))
		require.NoError(t, cloneMain(repoDir, remote))

		inProgress := filepath.Join(repoDir, "work-in-progress.txt")
		require.NoError(t, os.WriteFile(inProgress, []byte("half-written"), 0o644))

		require.NoError(t, CloneOrFetch(CloneParams{
			RepoDir: repoDir, URL: remote, Branch: "main", Depth: 50,
		}))

		_, err := os.Stat(inProgress)
		require.NoError(t, err, "uncommitted work must survive a fetch")
	})

	t.Run("deleted files removed on fetch", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		repoDir := filepath.Join(t.TempDir(), "clone")
		require.NoError(t, os.MkdirAll(repoDir, 0o755))

		addCommitToRemote(t, remote, "main", "ephemeral.txt", "will be deleted")
		require.NoError(t, cloneMain(repoDir, remote))

		_, err := os.Stat(filepath.Join(repoDir, "ephemeral.txt"))
		require.NoError(t, err, "ephemeral.txt should exist after clone")

		deleteFileInRemote(t, remote, "main", "ephemeral.txt")
		require.NoError(t, cloneMain(repoDir, remote))

		_, err = os.Stat(filepath.Join(repoDir, "ephemeral.txt"))
		require.True(t, os.IsNotExist(err), "ephemeral.txt should be gone after fetch+reset")
	})
}

func TestCommitLogSince(t *testing.T) {
	remote := createTestRemote(t, "main")
	repoDir := filepath.Join(t.TempDir(), "clone")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))
	require.NoError(t, cloneMain(repoDir, remote))

	firstSHA, err := headCommitSHA(repoDir, "main")
	require.NoError(t, err)

	addCommitToRemote(t, remote, "main", "a.txt", "commit A")
	addCommitToRemote(t, remote, "main", "b.txt", "commit B")
	require.NoError(t, cloneMain(repoDir, remote))

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
	require.NoError(t, cloneMain(repoDir, remote))

	logOutput, err := commitLogRecent(repoDir, "main", 2)
	require.NoError(t, err)

	lines := strings.Split(logOutput, "\n")
	require.Equal(t, 2, len(lines))
}

func TestHeadCommitSHA(t *testing.T) {
	remote := createTestRemote(t, "main")
	repoDir := filepath.Join(t.TempDir(), "clone")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))
	require.NoError(t, cloneMain(repoDir, remote))

	sha, err := headCommitSHA(repoDir, "main")
	require.NoError(t, err)
	require.Len(t, sha, 40, "should be a full 40-char SHA")
}

func TestCountFiles(t *testing.T) {
	t.Run("excludes .git directory", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		repoDir := filepath.Join(t.TempDir(), "clone")
		require.NoError(t, os.MkdirAll(repoDir, 0o755))
		require.NoError(t, cloneMain(repoDir, remote))

		count := countFiles(repoDir)
		// Should be 1 (init.txt), not 2 (init.txt + .git).
		require.Equal(t, 1, count)
	})
}

// --- helpers ---

// cloneMain clones or refreshes the test remote's main branch into repoDir,
// with mirror semantics: the working tree follows the remote exactly.
func cloneMain(repoDir, remote string) error {
	return CloneOrFetch(CloneParams{RepoDir: repoDir, URL: remote, Branch: "main", Depth: 50, ResetToRemote: true})
}

// cloneWithToken is cloneMain with authentication, for asserting where the
// token does and doesn't end up.
func cloneWithToken(repoDir, remote, token string) error {
	return CloneOrFetch(CloneParams{RepoDir: repoDir, URL: remote, Branch: "main", Token: token, Depth: 50, ResetToRemote: true})
}

// gitConfigContents reads the clone's .git/config.
func gitConfigContents(t *testing.T, repoDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoDir, ".git", "config"))
	require.NoError(t, err)
	return string(data)
}

// setRemoteURL points the clone's origin at url.
func setRemoteURL(t *testing.T, repoDir, url string) {
	t.Helper()
	out, err := exec.Command("git", "-C", repoDir, "remote", "set-url", "origin", url).CombinedOutput()
	require.NoError(t, err, "git remote set-url: %s", string(out))
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
