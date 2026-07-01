package router

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tclaw/internal/agent"
	"tclaw/internal/knowledgeproxy"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/user"
)

// knowledgeTokenSecretKey is the encrypted-store key holding the GitHub token,
// shared with repotools and devtools.
const knowledgeTokenSecretKey = "github_token"

// Default git identity used for agent commits when the config doesn't set one.
const (
	defaultKnowledgeCommitName  = "tclaw"
	defaultKnowledgeCommitEmail = "tclaw@users.noreply.github.com"
)

// startKnowledgeProxy launches the per-user git-auth proxy for the configured
// knowledge repo. The proxy injects the GitHub token server-side so the agent
// can pull/push without ever seeing it. The caller owns the returned server and
// must Stop it when the user session ends.
func startKnowledgeProxy(userID user.ID, kc *user.Knowledge, secretStore secret.Store) (*knowledgeproxy.Server, error) {
	repoPath, err := repoPathFromURL(kc.Repo)
	if err != nil {
		return nil, fmt.Errorf("derive repo path: %w", err)
	}

	server, err := knowledgeproxy.NewServer(knowledgeproxy.Config{
		RepoPath: repoPath,
		Token: func(ctx context.Context) (string, error) {
			return secretStore.Get(ctx, knowledgeTokenSecretKey)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("new knowledge proxy: %w", err)
	}

	if _, err := server.Start("127.0.0.1:0"); err != nil {
		return nil, fmt.Errorf("start knowledge proxy: %w", err)
	}
	slog.Info("knowledge proxy ready", "user", userID, "repo", repoPath, "addr", server.Addr())
	return server, nil
}

// knowledgeProvisionParams holds inputs for provisioning the vault clone.
type knowledgeProvisionParams struct {
	Dir         string
	RemoteURL   string
	Branch      string
	CommitName  string
	CommitEmail string
}

// provisionKnowledgeClone ensures the vault is cloned at Dir with the proxy
// remote and a git identity set. Idempotent: an existing clone is left as-is
// (the agent pulls at turn time) apart from re-asserting the identity. The
// remote is the token-free proxy URL, so no credentials land in .git/config.
func provisionKnowledgeClone(params knowledgeProvisionParams) error {
	dotGit := filepath.Join(params.Dir, ".git")
	if _, err := os.Stat(dotGit); err == nil {
		return configureGitIdentity(params.Dir, params.CommitName, params.CommitEmail)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dotGit, err)
	}

	if err := os.MkdirAll(filepath.Dir(params.Dir), 0o700); err != nil {
		return fmt.Errorf("create knowledge parent dir: %w", err)
	}

	slog.Info("cloning knowledge base", "dir", params.Dir, "branch", params.Branch)
	cmd := exec.Command("git", "-c", "core.hooksPath=/dev/null",
		"clone", "--branch", params.Branch, params.RemoteURL, params.Dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone knowledge base: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return configureGitIdentity(params.Dir, params.CommitName, params.CommitEmail)
}

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
