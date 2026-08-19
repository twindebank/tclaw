package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeedChannelKnowledge(t *testing.T) {
	t.Run("creates the channel index and the shared rules pool", func(t *testing.T) {
		memoryDir := t.TempDir()

		dir := seedChannelKnowledge(memoryDir, "email")

		require.Equal(t, filepath.Join(memoryDir, "channels", "email"), dir)

		index, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
		require.NoError(t, err)
		require.Contains(t, string(index), "email")
		require.Contains(t, string(index), "Rulebooks loaded on this channel")
		require.Contains(t, string(index), "Rulebooks available, not loaded")

		readme, err := os.ReadFile(filepath.Join(memoryDir, "rules", "README.md"))
		require.NoError(t, err)
		require.Contains(t, string(readme), "say yes first")
	})

	t.Run("leaves an existing index and README alone", func(t *testing.T) {
		memoryDir := t.TempDir()
		seedChannelKnowledge(memoryDir, "email")

		indexPath := filepath.Join(memoryDir, "channels", "email", "CLAUDE.md")
		readmePath := filepath.Join(memoryDir, "rules", "README.md")
		require.NoError(t, os.WriteFile(indexPath, []byte("# mine\n"), 0o600))
		require.NoError(t, os.WriteFile(readmePath, []byte("# mine too\n"), 0o600))

		seedChannelKnowledge(memoryDir, "email")

		index, err := os.ReadFile(indexPath)
		require.NoError(t, err)
		require.Equal(t, "# mine\n", string(index))
		readme, err := os.ReadFile(readmePath)
		require.NoError(t, err)
		require.Equal(t, "# mine too\n", string(readme))
	})

	t.Run("returns empty when there is no memory dir or channel", func(t *testing.T) {
		require.Empty(t, seedChannelKnowledge("", "email"))
		require.Empty(t, seedChannelKnowledge(t.TempDir(), ""))
	})
}
