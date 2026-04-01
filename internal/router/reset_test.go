package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/agent"
	"tclaw/internal/user"
)

// seedDir creates a directory with the given files. Each file gets "content" as its body.
func seedDir(t *testing.T, dir string, files ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	for _, f := range files {
		path := filepath.Join(dir, f)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("content"), 0o600))
	}
}

// dirFiles returns all file names in a directory (non-recursive).
func dirFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

type testDirs struct {
	memory    string
	home      string
	sessions  string
	state     string
	secrets   string
	mcpConfig string
}

func setupTestDirs(t *testing.T) testDirs {
	t.Helper()
	base := t.TempDir()
	dirs := testDirs{
		memory:    filepath.Join(base, "memory"),
		home:      filepath.Join(base, "home"),
		sessions:  filepath.Join(base, "sessions"),
		state:     filepath.Join(base, "state"),
		secrets:   filepath.Join(base, "secrets"),
		mcpConfig: filepath.Join(base, "mcp-config"),
	}

	seedDir(t, dirs.memory, "CLAUDE.md", "topic-a.md", "topic-b.md")
	seedDir(t, filepath.Join(dirs.home, ".claude"), "settings.json", "projects/session1.jsonl")
	seedDir(t, dirs.sessions, "main.sock", "telegram")
	seedDir(t, dirs.state, "connections", "schedules", "channels")
	seedDir(t, dirs.mcpConfig, "mcp-config.json")
	seedDir(t, dirs.secrets, "anthropic_api_key.enc", "conn_google_work.enc")

	return dirs
}

func (d testDirs) callReset(t *testing.T, level agent.ResetLevel) {
	t.Helper()
	err := resetUser(level, d.memory, d.home, d.sessions, d.state, d.secrets, d.mcpConfig)
	require.NoError(t, err)
}

func TestResetUser(t *testing.T) {
	t.Run("memories clears only memory dir", func(t *testing.T) {
		dirs := setupTestDirs(t)
		dirs.callReset(t, agent.ResetMemories)

		require.Empty(t, dirFiles(t, dirs.memory))

		require.NotEmpty(t, dirFiles(t, filepath.Join(dirs.home, ".claude")))
		require.NotEmpty(t, dirFiles(t, dirs.sessions))
		require.NotEmpty(t, dirFiles(t, dirs.state))
		require.NotEmpty(t, dirFiles(t, dirs.secrets))
		require.NotEmpty(t, dirFiles(t, dirs.mcpConfig))
	})

	t.Run("project clears claude state and sessions", func(t *testing.T) {
		dirs := setupTestDirs(t)
		dirs.callReset(t, agent.ResetProject)

		require.Empty(t, dirFiles(t, filepath.Join(dirs.home, ".claude")))
		require.Empty(t, dirFiles(t, dirs.sessions))

		require.NotEmpty(t, dirFiles(t, dirs.memory))
		require.NotEmpty(t, dirFiles(t, dirs.state))
		require.NotEmpty(t, dirFiles(t, dirs.secrets))
		require.NotEmpty(t, dirFiles(t, dirs.mcpConfig))
	})

	t.Run("all clears everything", func(t *testing.T) {
		dirs := setupTestDirs(t)
		dirs.callReset(t, agent.ResetAll)

		for _, dir := range []string{
			dirs.memory,
			filepath.Join(dirs.home, ".claude"),
			dirs.sessions,
			dirs.state,
			dirs.secrets,
			dirs.mcpConfig,
		} {
			require.Empty(t, dirFiles(t, dir), "expected %s to be empty", dir)
		}
	})

	t.Run("directories not removed after all", func(t *testing.T) {
		dirs := setupTestDirs(t)
		dirs.callReset(t, agent.ResetAll)

		for _, dir := range []string{dirs.memory, dirs.sessions, dirs.state, dirs.secrets, dirs.mcpConfig} {
			_, err := os.Stat(dir)
			require.NoError(t, err, "directory %s should still exist", dir)
		}
	})

	t.Run("missing directories are not an error", func(t *testing.T) {
		base := t.TempDir()
		err := resetUser(agent.ResetAll,
			filepath.Join(base, "nope-memory"),
			filepath.Join(base, "nope-home"),
			filepath.Join(base, "nope-sessions"),
			filepath.Join(base, "nope-state"),
			filepath.Join(base, "nope-secrets"),
			filepath.Join(base, "nope-mcp-config"),
		)
		require.NoError(t, err)
	})
}

func TestClearDirectoryContents(t *testing.T) {
	t.Run("removes all entries but keeps directory", func(t *testing.T) {
		dir := t.TempDir()
		seedDir(t, dir, "a.txt", "b.txt", "subdir/c.txt")

		require.NoError(t, clearDirectoryContents(dir))

		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Empty(t, entries)

		_, err = os.Stat(dir)
		require.NoError(t, err, "directory should still exist")
	})

	t.Run("non-existent directory returns nil", func(t *testing.T) {
		require.NoError(t, clearDirectoryContents("/nonexistent/path/12345"))
	})
}

func TestSeedUserMemory(t *testing.T) {
	t.Run("creates from scratch", func(t *testing.T) {
		base := t.TempDir()
		memoryDir := filepath.Join(base, "memory")
		homeDir := filepath.Join(base, "home")

		seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

		data, err := os.ReadFile(filepath.Join(memoryDir, "CLAUDE.md"))
		require.NoError(t, err)
		require.NotEmpty(t, data)

		target, err := os.Readlink(filepath.Join(homeDir, ".claude", "CLAUDE.md"))
		require.NoError(t, err)
		require.Equal(t, filepath.Join("..", "..", "memory", "CLAUDE.md"), target)
	})

	t.Run("idempotent does not overwrite", func(t *testing.T) {
		base := t.TempDir()
		memoryDir := filepath.Join(base, "memory")
		homeDir := filepath.Join(base, "home")

		seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

		claudePath := filepath.Join(memoryDir, "CLAUDE.md")
		require.NoError(t, os.WriteFile(claudePath, []byte("custom content"), 0o600))

		seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

		data, err := os.ReadFile(claudePath)
		require.NoError(t, err)
		require.Equal(t, "custom content", string(data))
	})

	t.Run("recovers after reset", func(t *testing.T) {
		base := t.TempDir()
		memoryDir := filepath.Join(base, "memory")
		homeDir := filepath.Join(base, "home")

		seedUserMemory(user.ID("testuser"), memoryDir, homeDir)
		require.NoError(t, clearDirectoryContents(memoryDir))
		require.NoError(t, clearDirectoryContents(filepath.Join(homeDir, ".claude")))

		seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

		_, err := os.Stat(filepath.Join(memoryDir, "CLAUDE.md"))
		require.NoError(t, err, "CLAUDE.md should be re-created")
		_, err = os.Lstat(filepath.Join(homeDir, ".claude", "CLAUDE.md"))
		require.NoError(t, err, "symlink should be re-created")
	})
}
