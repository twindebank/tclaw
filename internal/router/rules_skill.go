package router

import (
	_ "embed"
	"log/slog"
	"os"
	"path/filepath"

	"tclaw/internal/user"
)

// rulesSkill explains the rulebook pool: what belongs in one, the entry shape,
// how a channel decides what it loads, and the propose-and-confirm route by
// which a rule changes. It is a skill rather than more system prompt because it
// is needed when writing a rule down, not on every turn.
//
//go:embed rules_skill.md
var rulesSkill string

// seedRulesSkill writes the channel-rules skill into the user's Claude Code
// skills directory. Overwritten on each call so an updated skill ships with a
// deploy and a reset that cleared home/.claude/ is repaired.
func seedRulesSkill(userID user.ID, homeDir string) {
	skillDir := filepath.Join(homeDir, ".claude", "skills", "channel-rules")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		slog.Error("failed to create channel-rules skill dir", "user", userID, "err", err)
		return
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(rulesSkill), 0o600); err != nil {
		slog.Error("failed to seed channel-rules SKILL.md", "user", userID, "err", err)
	}
}
