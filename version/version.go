package version

import (
	"os/exec"
	"strings"
)

// Commit is the git commit hash embedded at build time via ldflags:
//
//	go build -ldflags "-X tclaw/version.Commit=$(git rev-parse --short HEAD)"
//
// Set to "dev" when building without ldflags (e.g. local go run).
var Commit = "dev"

// Repository is the GitHub owner/repo slug (e.g. "owner/repo") embedded at
// build time via ldflags or the GITHUB_REPOSITORY env var baked into the
// Docker image. Empty when building without ldflags (e.g. local go run) —
// callers should fall back to deriving it from git remote.
var Repository = ""

// RepoSlug returns the GitHub owner/repo slug. It tries, in order:
//  1. The build-time Repository variable (set via ldflags in Docker builds)
//  2. Parsing `git remote get-url origin` (works for local dev)
//
// Returns empty string if the repo slug cannot be determined.
func RepoSlug() string {
	if Repository != "" {
		return Repository
	}
	return repoSlugFromGitRemote()
}

// repoSlugFromGitRemote derives owner/repo from the origin remote URL.
// Handles both HTTPS (https://github.com/owner/repo.git) and SSH
// (git@github.com:owner/repo.git) formats.
func repoSlugFromGitRemote() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return ParseRepoSlug(strings.TrimSpace(string(out)))
}

// ParseRepoSlug extracts the owner/repo slug from a GitHub remote URL.
// Returns empty string if the URL doesn't look like a GitHub repo.
func ParseRepoSlug(remoteURL string) string {
	slug := remoteURL

	// SSH format: git@github.com:owner/repo.git
	if after, ok := strings.CutPrefix(slug, "git@github.com:"); ok {
		slug = after
	} else if after, ok := strings.CutPrefix(slug, "https://github.com/"); ok {
		slug = after
	} else {
		return ""
	}

	slug = strings.TrimSuffix(slug, ".git")
	if !strings.Contains(slug, "/") {
		return ""
	}
	return slug
}
