package router

import (
	_ "embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"tclaw/internal/user"
)

// gwsSkillsSourceDir is where the Dockerfile bakes `gws generate-skills` output.
// It's absent in local (non-container) dev, in which case seeding is skipped —
// the agent still has the trimmed google_workspace tool description and the
// google_workspace_schema tool to discover command syntax.
const gwsSkillsSourceDir = "/etc/tclaw/gws-skills"

// gwsTclawSkill is the tclaw-authored authority skill that explains how to
// invoke gws via the google_workspace MCP tool (rather than the shell) and
// records tclaw-specific API gotchas. It overrides any generated copy so the
// mapping guidance always wins.
//
//go:embed gws_tclaw_skill.md
var gwsTclawSkill string

// seedGWSSkills copies the Google Workspace CLI skills baked into the image into
// the user's Claude Code skills directory, then writes the tclaw authority skill
// on top. The claude CLI auto-discovers skills from home/.claude/skills/, so this
// lets the agent learn gws command syntax from skills rather than a hand-maintained
// tool description. Overwrites on each call so gws-version updates ship and a reset
// that cleared home/.claude/ is repaired.
func seedGWSSkills(userID user.ID, homeDir string) {
	if _, err := os.Stat(gwsSkillsSourceDir); err != nil {
		// No baked skills (e.g. local dev) — nothing to seed, not an error.
		slog.Debug("gws skills source dir absent, skipping seed", "user", userID, "dir", gwsSkillsSourceDir, "err", err)
		return
	}

	skillsDir := filepath.Join(homeDir, ".claude", "skills")
	if err := copySkillTree(gwsSkillsSourceDir, skillsDir); err != nil {
		slog.Error("failed to seed gws skills", "user", userID, "err", err)
		return
	}

	// The tclaw authority skill must land last so it overrides any generated
	// gws-shared/entry guidance about running gws in the shell.
	tclawSkillDir := filepath.Join(skillsDir, "gws-tclaw")
	if err := os.MkdirAll(tclawSkillDir, 0o700); err != nil {
		slog.Error("failed to create gws-tclaw skill dir", "user", userID, "err", err)
		return
	}
	if err := os.WriteFile(filepath.Join(tclawSkillDir, "SKILL.md"), []byte(gwsTclawSkill), 0o600); err != nil {
		slog.Error("failed to seed gws-tclaw SKILL.md", "user", userID, "err", err)
	}
}

// copySkillTree recursively copies every file under srcDir into destDir,
// overwriting existing files so re-seeding picks up updated skills.
func copySkillTree(srcDir, destDir string) error {
	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("relativize %q: %w", path, err)
		}
		target := filepath.Join(destDir, rel)

		if d.IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("mkdir %q: %w", target, err)
			}
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %q: %w", path, err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return fmt.Errorf("write %q: %w", target, err)
		}
		return nil
	})
}
