package router

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"tclaw/internal/agent"
	"tclaw/internal/user"
)

// resetUser clears user data according to the given level.
//
// Directory mapping:
//
//	ResetMemories → clears memory/ (CLAUDE.md, topic files)
//	ResetProject  → clears home/.claude/ (Claude Code state) + sessions/
//	ResetAll      → clears memory/ + home/.claude/ + sessions/ + state/ + secrets/
func resetUser(level agent.ResetLevel, memoryDir, homeDir, sessionsDir, stateDir, secretsDir, mcpConfigDir string) error {
	switch level {
	case agent.ResetMemories:
		return clearDirectoryContents(memoryDir)

	case agent.ResetProject:
		claudeDir := filepath.Join(homeDir, ".claude")
		if err := clearDirectoryContents(claudeDir); err != nil {
			return fmt.Errorf("clear claude state: %w", err)
		}
		if err := clearDirectoryContents(sessionsDir); err != nil {
			return fmt.Errorf("clear sessions: %w", err)
		}
		return nil

	case agent.ResetAll:
		dirs := []struct {
			path string
			name string
		}{
			{memoryDir, "memory"},
			{filepath.Join(homeDir, ".claude"), "claude state"},
			{sessionsDir, "sessions"},
			{stateDir, "state"},
			{secretsDir, "secrets"},
			{mcpConfigDir, "mcp-config"},
		}
		for _, d := range dirs {
			if err := clearDirectoryContents(d.path); err != nil {
				return fmt.Errorf("clear %s: %w", d.name, err)
			}
		}
		return nil

	default:
		return fmt.Errorf("unknown reset level: %d", level)
	}
}

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

// clearDirectoryContents removes all entries inside a directory without
// removing the directory itself. Returns nil if the directory doesn't exist.
func clearDirectoryContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}
