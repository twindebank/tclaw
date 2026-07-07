package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/user"
)

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

	t.Run("recreates missing files", func(t *testing.T) {
		base := t.TempDir()
		memoryDir := filepath.Join(base, "memory")
		homeDir := filepath.Join(base, "home")

		seedUserMemory(user.ID("testuser"), memoryDir, homeDir)
		require.NoError(t, os.RemoveAll(memoryDir))
		require.NoError(t, os.RemoveAll(filepath.Join(homeDir, ".claude")))

		seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

		_, err := os.Stat(filepath.Join(memoryDir, "CLAUDE.md"))
		require.NoError(t, err, "CLAUDE.md should be re-created")
		_, err = os.Lstat(filepath.Join(homeDir, ".claude", "CLAUDE.md"))
		require.NoError(t, err, "symlink should be re-created")
	})
}
