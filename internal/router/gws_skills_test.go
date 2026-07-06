package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCopySkillTree(t *testing.T) {
	t.Run("copies a nested skill tree", func(t *testing.T) {
		src := t.TempDir()
		writeFile(t, filepath.Join(src, "gws-gmail", "SKILL.md"), "gmail skill")
		writeFile(t, filepath.Join(src, "recipe-label", "SKILL.md"), "recipe skill")

		dest := t.TempDir()
		require.NoError(t, copySkillTree(src, dest))

		require.Equal(t, "gmail skill", readFile(t, filepath.Join(dest, "gws-gmail", "SKILL.md")))
		require.Equal(t, "recipe skill", readFile(t, filepath.Join(dest, "recipe-label", "SKILL.md")))
	})

	t.Run("overwrites existing files without touching siblings", func(t *testing.T) {
		src := t.TempDir()
		writeFile(t, filepath.Join(src, "gws-gmail", "SKILL.md"), "new content")

		dest := t.TempDir()
		// Pre-existing stale copy of the same skill, plus an unrelated skill.
		writeFile(t, filepath.Join(dest, "gws-gmail", "SKILL.md"), "old content")
		writeFile(t, filepath.Join(dest, "knowledge", "SKILL.md"), "knowledge skill")

		require.NoError(t, copySkillTree(src, dest))

		require.Equal(t, "new content", readFile(t, filepath.Join(dest, "gws-gmail", "SKILL.md")))
		// A sibling skill outside the source tree is left untouched.
		require.Equal(t, "knowledge skill", readFile(t, filepath.Join(dest, "knowledge", "SKILL.md")))
	})

	t.Run("errors when source is missing", func(t *testing.T) {
		err := copySkillTree(filepath.Join(t.TempDir(), "does-not-exist"), t.TempDir())
		require.Error(t, err)
	})
}

func TestSeedGWSSkills(t *testing.T) {
	t.Run("embedded tclaw skill is well-formed", func(t *testing.T) {
		// The authority skill must carry the frontmatter name the tool description
		// and other skills reference, and the MCP-mapping guidance.
		require.Contains(t, gwsTclawSkill, "name: gws-tclaw")
		require.Contains(t, gwsTclawSkill, "google_workspace")
	})

	t.Run("skips silently when no baked skills are present", func(t *testing.T) {
		home := t.TempDir()
		// gwsSkillsSourceDir is absent in test/dev — seeding must be a no-op, not a panic.
		seedGWSSkills("alice", home)

		_, err := os.Stat(filepath.Join(home, ".claude", "skills"))
		require.True(t, os.IsNotExist(err), "no skills dir should be created without a source")
	})
}

// --- helpers ---

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.TrimSpace(string(data))
}
