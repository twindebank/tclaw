package router

import (
	"log/slog"
	"os"
	"path/filepath"

	"tclaw/internal/agent"
	"tclaw/internal/user"
)

// seedUserMemory ensures memory/CLAUDE.md exists, the home/.claude/CLAUDE.md
// symlink points to it, and settings.json exists with safe defaults.
// Idempotent — only writes if the file/link doesn't exist.
func seedUserMemory(userID user.ID, memoryDir, homeDir string) {
	memoryMDPath := filepath.Join(memoryDir, "CLAUDE.md")
	if _, statErr := os.Stat(memoryMDPath); os.IsNotExist(statErr) {
		if mkErr := os.MkdirAll(memoryDir, 0o700); mkErr != nil {
			slog.Error("failed to create memory dir", "user", userID, "err", mkErr)
		} else if wErr := os.WriteFile(memoryMDPath, []byte(agent.DefaultMemoryTemplate), 0o600); wErr != nil {
			slog.Error("failed to seed CLAUDE.md", "user", userID, "err", wErr)
		} else {
			slog.Debug("seeded memory/CLAUDE.md", "user", userID, "path", memoryMDPath)
		}
	}

	claudeDir := filepath.Join(homeDir, ".claude")
	symlinkPath := filepath.Join(claudeDir, "CLAUDE.md")
	if _, statErr := os.Lstat(symlinkPath); os.IsNotExist(statErr) {
		if mkErr := os.MkdirAll(claudeDir, 0o700); mkErr != nil {
			slog.Error("failed to create .claude dir", "user", userID, "err", mkErr)
		} else if linkErr := os.Symlink(filepath.Join("..", "..", "memory", "CLAUDE.md"), symlinkPath); linkErr != nil {
			slog.Error("failed to create CLAUDE.md symlink", "user", userID, "err", linkErr)
		} else {
			slog.Debug("created CLAUDE.md symlink", "user", userID, "link", symlinkPath)
		}
	}

	// Pre-create settings.json with safe defaults (empty object) to prevent
	// the agent from creating its own with malicious SessionStart hooks.
	// The sandbox mounts this file read-only (see handle.go ReadOnlyOverlay),
	// but we also need to seed it for local dev where there's no sandbox.
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if _, statErr := os.Stat(settingsPath); os.IsNotExist(statErr) {
		if mkErr := os.MkdirAll(claudeDir, 0o700); mkErr != nil {
			slog.Error("failed to create .claude dir for settings", "user", userID, "err", mkErr)
		} else if wErr := os.WriteFile(settingsPath, []byte("{}\n"), 0o600); wErr != nil {
			slog.Error("failed to seed settings.json", "user", userID, "err", wErr)
		} else {
			slog.Debug("seeded settings.json", "user", userID, "path", settingsPath)
		}
	}
}
