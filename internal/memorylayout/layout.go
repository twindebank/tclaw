// Package memorylayout names the paths and environment variables shared between
// the agent loop and the hook binary. Both need to agree on where rulebooks live
// and how a hook learns which channel a turn belongs to, and the hook binary runs
// on every tool call, so it stays out of the heavier agent package.
package memorylayout

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// RulesDirName holds every rulebook, for every channel. One pool rather than
	// a directory per channel: scoping decides what loads automatically, never
	// what a channel is allowed to read.
	RulesDirName = "rules"

	// ChannelsDirName holds one directory per channel, each with the CLAUDE.md
	// that is loaded only on that channel's turns.
	ChannelsDirName = "channels"

	// EnvMemoryDir tells the hook binary where the memory directory is. It is set
	// on the claude subprocess, so a hook inherits it and the agent cannot change
	// it for its own hooks.
	EnvMemoryDir = "TCLAW_MEMORY_DIR"

	// EnvChannel names the channel whose turn is running.
	EnvChannel = "TCLAW_CHANNEL"
)

// RulesDir is the rulebook pool inside a memory directory.
func RulesDir(memoryDir string) string {
	return filepath.Join(memoryDir, RulesDirName)
}

// ChannelDir is one channel's knowledge directory inside a memory directory.
func ChannelDir(memoryDir, channelName string) string {
	return filepath.Join(memoryDir, ChannelsDirName, channelName)
}

// InRules reports whether path is a file inside the rulebook pool. Both sides are
// symlink-resolved before comparing: macOS reports one directory as both /var/...
// and /private/var/..., and comparing them raw makes the same file look like it is
// somewhere else entirely.
func InRules(memoryDir, path string) bool {
	if memoryDir == "" || path == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rules := resolve(RulesDir(memoryDir))
	if rules == "" {
		return false
	}
	return strings.HasPrefix(resolve(abs), rules+string(os.PathSeparator))
}

// resolve returns path with symlinks resolved, walking up to the nearest existing
// parent so a file that does not exist yet still resolves to the right directory.
func resolve(path string) string {
	dir, base := filepath.Split(path)
	dir = filepath.Clean(dir)
	for {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, base)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Join(dir, base)
		}
		base = filepath.Join(filepath.Base(dir), base)
		dir = parent
	}
}
