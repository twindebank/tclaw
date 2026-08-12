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
