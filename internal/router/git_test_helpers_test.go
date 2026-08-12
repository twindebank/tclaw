package router

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Shared git helpers for router tests that work against real repositories —
// clone provisioning, the vault sync and the repo lifecycle sweep all need a
// remote to talk to.

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
