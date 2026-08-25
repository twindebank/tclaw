package router

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tclaw/internal/agent"
	"tclaw/internal/memorylayout"
	"tclaw/internal/user"
)

// Default git identity used for agent commits when the config doesn't set one.
const (
	defaultKnowledgeCommitName  = "tclaw"
	defaultKnowledgeCommitEmail = "tclaw@users.noreply.github.com"
)

// configureGitIdentity sets the local commit identity for the clone, applying
// tclaw defaults when the config leaves them empty.
func configureGitIdentity(dir, name, email string) error {
	if name == "" {
		name = defaultKnowledgeCommitName
	}
	if email == "" {
		email = defaultKnowledgeCommitEmail
	}
	if out, err := exec.Command("git", "-C", dir, "config", "user.name", name).CombinedOutput(); err != nil {
		return fmt.Errorf("git config user.name: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if out, err := exec.Command("git", "-C", dir, "config", "user.email", email).CombinedOutput(); err != nil {
		return fmt.Errorf("git config user.email: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// seedKnowledgeSkill writes the knowledge SKILL.md into the user's Claude Code
// skills directory, rendering the vault path. Overwrites on each call so
// template updates ship and a reset that cleared home/.claude/ is repaired.
func seedKnowledgeSkill(userID user.ID, homeDir, knowledgeDir string) {
	skillDir := filepath.Join(homeDir, ".claude", "skills", "knowledge")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		slog.Error("failed to create knowledge skill dir", "user", userID, "err", err)
		return
	}
	content := strings.ReplaceAll(agent.KnowledgeSkillTemplate, "{{path}}", knowledgeDir)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(content), 0o600); err != nil {
		slog.Error("failed to seed knowledge SKILL.md", "user", userID, "err", err)
	}
}

// seedKnowledgeExtrasParams says which user is being seeded and which vault
// directories install into their Claude config directory.
type seedKnowledgeExtrasParams struct {
	UserID       user.ID
	HomeDir      string
	KnowledgeDir string
	ClaudeDirs   map[string]string
}

// seedKnowledgeExtras installs the skills, subagents and rule files the vault
// carries, so a playbook written on a laptop is available here too.
func seedKnowledgeExtras(params seedKnowledgeExtrasParams) {
	// No new capability, despite copying out of a writable clone: the agent can
	// already write these directories directly. What this adds is the vault as
	// the place they are kept, so a change made anywhere reaches every machine.
	for name, relative := range params.ClaudeDirs {
		source := filepath.Join(params.KnowledgeDir, relative)
		if _, err := os.Stat(source); err != nil {
			// A vault that has not been cloned yet, or does not carry this
			// directory. Neither is an error worth stopping a boot for.
			slog.Debug("vault has no directory to install", "user", params.UserID, "name", name, "dir", source, "err", err)
			continue
		}
		target := filepath.Join(params.HomeDir, memorylayout.ConfigDirName, name)
		if err := copySkillTree(source, target); err != nil {
			slog.Error("failed to install from vault", "user", params.UserID, "name", name, "dir", source, "err", err)
			continue
		}
		slog.Debug("installed from vault", "user", params.UserID, "name", name, "dir", source)
	}
}

// repoPathFromURL extracts the "owner/repo" path from a repo URL, dropping any
// scheme, host, leading slash, and ".git" suffix.
func repoPathFromURL(repoURL string) (string, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", fmt.Errorf("parse repo url %q: %w", repoURL, err)
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if path == "" {
		return "", fmt.Errorf("no repo path in %q", repoURL)
	}
	return path, nil
}
