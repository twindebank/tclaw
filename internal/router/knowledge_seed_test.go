package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeedKnowledgeExtras(t *testing.T) {
	t.Run("installs every directory the vault carries", func(t *testing.T) {
		homeDir, knowledgeDir := t.TempDir(), t.TempDir()
		writeSeedFile(t, filepath.Join(knowledgeDir, "vault-claude", "skills", "note-taking", "SKILL.md"), "# a skill")
		writeSeedFile(t, filepath.Join(knowledgeDir, "vault-claude", "agents", "reviewer.md"), "# a subagent")
		writeSeedFile(t, filepath.Join(knowledgeDir, "vault-claude", "rules", "general.md"), "# rules")

		seedKnowledgeExtras(seedKnowledgeExtrasParams{
			UserID:       "testuser",
			HomeDir:      homeDir,
			KnowledgeDir: knowledgeDir,
			ClaudeDirs: map[string]string{
				"skills":   "vault-claude/skills",
				"agents":   "vault-claude/agents",
				"patterns": "vault-claude/rules",
			},
		})

		require.Equal(t, "# a skill", readSeedFile(t, filepath.Join(homeDir, ".claude", "skills", "note-taking", "SKILL.md")))
		require.Equal(t, "# a subagent", readSeedFile(t, filepath.Join(homeDir, ".claude", "agents", "reviewer.md")))
		require.Equal(t, "# rules", readSeedFile(t, filepath.Join(homeDir, ".claude", "patterns", "general.md")))
	})

	t.Run("overwrites an earlier copy so an edited skill ships", func(t *testing.T) {
		homeDir, knowledgeDir := t.TempDir(), t.TempDir()
		writeSeedFile(t, filepath.Join(homeDir, ".claude", "skills", "note-taking", "SKILL.md"), "# stale")
		writeSeedFile(t, filepath.Join(knowledgeDir, "skills", "note-taking", "SKILL.md"), "# current")

		seedKnowledgeExtras(seedKnowledgeExtrasParams{
			UserID:       "testuser",
			HomeDir:      homeDir,
			KnowledgeDir: knowledgeDir,
			ClaudeDirs:   map[string]string{"skills": "skills"},
		})

		require.Equal(t, "# current", readSeedFile(t, filepath.Join(homeDir, ".claude", "skills", "note-taking", "SKILL.md")))
	})

	t.Run("skips a directory the vault does not carry", func(t *testing.T) {
		homeDir, knowledgeDir := t.TempDir(), t.TempDir()
		writeSeedFile(t, filepath.Join(knowledgeDir, "skills", "note-taking", "SKILL.md"), "# a skill")

		seedKnowledgeExtras(seedKnowledgeExtrasParams{
			UserID:       "testuser",
			HomeDir:      homeDir,
			KnowledgeDir: knowledgeDir,
			ClaudeDirs:   map[string]string{"skills": "skills", "agents": "vault-claude/agents"},
		})

		require.Equal(t, "# a skill", readSeedFile(t, filepath.Join(homeDir, ".claude", "skills", "note-taking", "SKILL.md")),
			"one missing directory must not stop the rest")
		_, err := os.Stat(filepath.Join(homeDir, ".claude", "agents"))
		require.True(t, os.IsNotExist(err), "nothing to copy should leave nothing behind")
	})

	t.Run("links a directory so an edit reaches the vault", func(t *testing.T) {
		homeDir, knowledgeDir := t.TempDir(), t.TempDir()
		writeSeedFile(t, filepath.Join(knowledgeDir, "vault-claude", "rules", "general.md"), "# rules")

		seedKnowledgeExtras(seedKnowledgeExtrasParams{
			UserID:       "testuser",
			HomeDir:      homeDir,
			KnowledgeDir: knowledgeDir,
			ClaudeLinks:  map[string]string{"patterns": "vault-claude/rules"},
		})

		// A copy would read the same and be thrown away at the next boot, so what
		// matters is that a write through the link lands in the clone.
		linked := filepath.Join(homeDir, ".claude", "patterns", "general.md")
		require.Equal(t, "# rules", readSeedFile(t, linked))
		require.NoError(t, os.WriteFile(linked, []byte("# edited"), 0o600))
		require.Equal(t, "# edited", readSeedFile(t, filepath.Join(knowledgeDir, "vault-claude", "rules", "general.md")))
	})

	t.Run("follows a vault directory that moved", func(t *testing.T) {
		homeDir, knowledgeDir := t.TempDir(), t.TempDir()
		writeSeedFile(t, filepath.Join(knowledgeDir, "old", "general.md"), "# old")
		writeSeedFile(t, filepath.Join(knowledgeDir, "new", "general.md"), "# new")
		params := seedKnowledgeExtrasParams{
			UserID: "testuser", HomeDir: homeDir, KnowledgeDir: knowledgeDir,
			ClaudeLinks: map[string]string{"patterns": "old"},
		}
		seedKnowledgeExtras(params)

		params.ClaudeLinks = map[string]string{"patterns": "new"}
		seedKnowledgeExtras(params)

		require.Equal(t, "# new", readSeedFile(t, filepath.Join(homeDir, ".claude", "patterns", "general.md")))
	})

	t.Run("leaves a real directory alone rather than replacing it", func(t *testing.T) {
		homeDir, knowledgeDir := t.TempDir(), t.TempDir()
		writeSeedFile(t, filepath.Join(knowledgeDir, "rules", "general.md"), "# vault")
		// Something else put files here, so they are not this function's to delete.
		writeSeedFile(t, filepath.Join(homeDir, ".claude", "patterns", "local.md"), "# local")

		seedKnowledgeExtras(seedKnowledgeExtrasParams{
			UserID: "testuser", HomeDir: homeDir, KnowledgeDir: knowledgeDir,
			ClaudeLinks: map[string]string{"patterns": "rules"},
		})

		require.Equal(t, "# local", readSeedFile(t, filepath.Join(homeDir, ".claude", "patterns", "local.md")))
	})

	t.Run("does nothing when the config names no directories", func(t *testing.T) {
		homeDir, knowledgeDir := t.TempDir(), t.TempDir()
		writeSeedFile(t, filepath.Join(knowledgeDir, "vault-claude", "skills", "note-taking", "SKILL.md"), "# a skill")

		seedKnowledgeExtras(seedKnowledgeExtrasParams{
			UserID:       "testuser",
			HomeDir:      homeDir,
			KnowledgeDir: knowledgeDir,
		})

		_, err := os.Stat(filepath.Join(homeDir, ".claude", "skills"))
		require.True(t, os.IsNotExist(err), "an unset directory must not be guessed at")
	})
}

// --- helpers ---

func writeSeedFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func readSeedFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}
