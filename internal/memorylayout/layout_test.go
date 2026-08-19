package memorylayout_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/memorylayout"
)

func TestInRules(t *testing.T) {
	t.Run("recognises a rulebook, including one not written yet", func(t *testing.T) {
		memoryDir := t.TempDir()
		require.NoError(t, os.MkdirAll(memorylayout.RulesDir(memoryDir), 0o700))

		require.True(t, memorylayout.InRules(memoryDir, filepath.Join(memoryDir, "rules", "invoices.md")))
		require.True(t, memorylayout.InRules(memoryDir, filepath.Join(memoryDir, "rules", "not-created-yet.md")))
	})

	t.Run("rejects everything outside the pool", func(t *testing.T) {
		memoryDir := t.TempDir()
		require.NoError(t, os.MkdirAll(memorylayout.RulesDir(memoryDir), 0o700))

		cases := map[string]string{
			"a memory file":       filepath.Join(memoryDir, "CLAUDE.md"),
			"a channel index":     filepath.Join(memoryDir, "channels", "admin", "CLAUDE.md"),
			"the pool itself":     memorylayout.RulesDir(memoryDir),
			"a sibling directory": filepath.Join(memoryDir, "rules-archive", "old.md"),
			"somewhere else":      "/etc/passwd",
		}
		for name, path := range cases {
			require.False(t, memorylayout.InRules(memoryDir, path), name)
		}
	})

	t.Run("matches through a symlinked memory dir", func(t *testing.T) {
		// macOS reports a temp dir as both /var/... and /private/var/..., so a
		// raw prefix check makes the same file look like it is somewhere else.
		real := t.TempDir()
		require.NoError(t, os.MkdirAll(memorylayout.RulesDir(real), 0o700))
		link := filepath.Join(t.TempDir(), "memory")
		require.NoError(t, os.Symlink(real, link))

		require.True(t, memorylayout.InRules(link, filepath.Join(real, "rules", "invoices.md")))
		require.True(t, memorylayout.InRules(real, filepath.Join(link, "rules", "invoices.md")))
	})

	t.Run("says no when either side is missing", func(t *testing.T) {
		require.False(t, memorylayout.InRules("", "/tmp/rules/x.md"))
		require.False(t, memorylayout.InRules("/tmp/memory", ""))
	})
}
