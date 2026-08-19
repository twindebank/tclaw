package router

import (
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"tclaw/internal/agent"
	"tclaw/internal/hooks"
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

	seedHooks(userID, settingsPath)
}

// seedHooks writes tclaw's hook registrations into the user's settings.json on
// every boot, so a hook added to the manifest reaches an existing user and one
// removed from it stops running. Every other key in the file is preserved.
//
// Registration belongs to tclaw rather than the agent: the file is mounted
// read-only in the sandbox, which is what stops a prompt injection from turning
// hooks off or pointing one somewhere else.
func seedHooks(userID user.ID, settingsPath string) {
	binary, err := exec.LookPath(hooks.BinaryName)
	if err != nil {
		// The agent runs unhooked rather than failing every tool call on a missing
		// binary. Said at WARN because an absent guard looks identical to a guard
		// with nothing to complain about: without this line, rulebooks would appear
		// to be enforced while the agent could rewrite any of them.
		slog.Warn("hook binary not found — rulebooks are NOT enforced this run; build it with `tclaw install`",
			"user", userID, "binary", hooks.BinaryName, "err", err)
		return
	}

	block, err := hooks.SettingsBlock(binary)
	if err != nil {
		slog.Error("failed to build hook registrations", "user", userID, "err", err)
		return
	}

	settings := map[string]json.RawMessage{}
	raw, err := os.ReadFile(settingsPath)
	switch {
	case os.IsNotExist(err):
		// A fresh user, or a home directory that was reset: start from nothing.
	case err != nil:
		slog.Error("failed to read settings.json for hook registration", "user", userID, "err", err)
		return
	default:
		if unmarshalErr := json.Unmarshal(raw, &settings); unmarshalErr != nil {
			slog.Error("settings.json is not valid JSON, leaving it alone", "user", userID, "err", unmarshalErr)
			return
		}
	}

	settings["hooks"] = block
	merged, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		slog.Error("failed to encode settings.json", "user", userID, "err", err)
		return
	}
	if err := os.WriteFile(settingsPath, append(merged, '\n'), 0o600); err != nil {
		slog.Error("failed to write hook registrations", "user", userID, "err", err)
		return
	}
	slog.Debug("registered hooks", "user", userID, "count", len(hooks.Manifest), "binary", binary)
}
