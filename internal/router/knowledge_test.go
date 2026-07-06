package router

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProvisionKnowledgeClone(t *testing.T) {
	t.Run("clones the vault and sets the commit identity", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		dir := filepath.Join(t.TempDir(), "knowledge")

		err := provisionKnowledgeClone(knowledgeProvisionParams{
			Dir:         dir,
			RemoteURL:   remote,
			Branch:      "main",
			CommitName:  "Test User",
			CommitEmail: "test@example.com",
		})
		require.NoError(t, err)

		require.DirExists(t, filepath.Join(dir, ".git"))
		require.FileExists(t, filepath.Join(dir, "index.md"))
		require.Equal(t, "Test User", gitConfigValue(t, dir, "user.name"))
		require.Equal(t, "test@example.com", gitConfigValue(t, dir, "user.email"))
	})

	t.Run("defaults the identity when unset", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		dir := filepath.Join(t.TempDir(), "knowledge")

		err := provisionKnowledgeClone(knowledgeProvisionParams{
			Dir:       dir,
			RemoteURL: remote,
			Branch:    "main",
		})
		require.NoError(t, err)

		require.Equal(t, defaultKnowledgeCommitName, gitConfigValue(t, dir, "user.name"))
		require.Equal(t, defaultKnowledgeCommitEmail, gitConfigValue(t, dir, "user.email"))
	})

	t.Run("is idempotent and preserves local commits", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		dir := filepath.Join(t.TempDir(), "knowledge")
		params := knowledgeProvisionParams{Dir: dir, RemoteURL: remote, Branch: "main"}

		require.NoError(t, provisionKnowledgeClone(params))

		// Simulate the agent creating and committing a note locally.
		require.NoError(t, os.WriteFile(filepath.Join(dir, "note.md"), []byte("hi"), 0o644))
		gitRun(t, dir, "add", "note.md")
		gitRun(t, dir, "commit", "-m", "agent note")

		// A second provision must not wipe the local commit (no reset --hard).
		require.NoError(t, provisionKnowledgeClone(params))

		require.FileExists(t, filepath.Join(dir, "note.md"))
		out := gitOutput(t, dir, "log", "--oneline")
		require.Contains(t, out, "agent note")
	})

	t.Run("re-points origin at the current proxy URL on re-provision", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		dir := filepath.Join(t.TempDir(), "knowledge")

		// First boot: clone against a proxy URL that mimics a now-dead port.
		stalePort := "http://127.0.0.1:37557/owner/knowledge-base.git"
		require.NoError(t, provisionKnowledgeClone(knowledgeProvisionParams{
			Dir: dir, RemoteURL: remote, Branch: "main",
		}))
		gitRun(t, dir, "remote", "set-url", "origin", stalePort)
		require.Equal(t, stalePort, gitConfigValue(t, dir, "remote.origin.url"))

		// Second boot: proxy came up on a fresh port, so provision must re-point.
		freshRemote := "http://127.0.0.1:41000/owner/knowledge-base.git"
		require.NoError(t, provisionKnowledgeClone(knowledgeProvisionParams{
			Dir: dir, RemoteURL: freshRemote, Branch: "main",
		}))
		require.Equal(t, freshRemote, gitConfigValue(t, dir, "remote.origin.url"))
	})
}

func TestRepoPathFromURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{"https with .git", "https://github.com/owner/knowledge-base.git", "owner/knowledge-base", false},
		{"https without .git", "https://github.com/owner/knowledge-base", "owner/knowledge-base", false},
		{"trailing slash", "https://github.com/owner/knowledge-base/", "owner/knowledge-base", false},
		{"no path", "https://github.com", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repoPathFromURL(tt.url)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// --- helpers ---

func createTestRemote(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "--initial-branch", branch)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.md"), []byte("# vault"), 0o644))
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "initial commit")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = gitOutput(t, dir, args...)
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, string(out))
	return string(out)
}

func gitConfigValue(t *testing.T, dir, key string) string {
	t.Helper()
	return strings.TrimSpace(gitOutput(t, dir, "config", key))
}
