package router

import (
	"fmt"
	"os"
	"path/filepath"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
)

// UserDirs holds the per-user directory paths.
type UserDirs struct {
	Base      string // root for this user (e.g. /data/tclaw/theo)
	Home      string // Claude Code's territory (HOME env var)
	Memory    string // agent's sandboxed memory (CWD + --add-dir)
	State     string // tclaw persistent data (not mounted in sandbox)
	Sessions  string // Claude CLI session IDs per channel
	Secrets   string // encrypted credentials
	MCPConfig string // MCP config files (mounted read-only in sandbox)
}

// NewUserDirs computes all directory paths from a base directory and user ID.
func NewUserDirs(baseDir string, userID string) UserDirs {
	userDir := filepath.Join(baseDir, userID)
	return UserDirs{
		Base:      userDir,
		Home:      filepath.Join(userDir, "home"),
		Memory:    filepath.Join(userDir, "memory"),
		State:     filepath.Join(userDir, "state"),
		Sessions:  filepath.Join(userDir, "sessions"),
		Secrets:   filepath.Join(userDir, "secrets"),
		MCPConfig: filepath.Join(userDir, "mcp-config"),
	}
}

// EnsureMediaDir creates the media subdirectory for Telegram file downloads.
func (d UserDirs) EnsureMediaDir() error {
	return os.MkdirAll(filepath.Join(d.Memory, "media"), 0o755)
}

// UserStores groups the per-user persistent stores.
type UserStores struct {
	State        store.Store
	Session      store.Store
	Secret       secret.Store
	RuntimeState *channel.RuntimeStateStore
}

// NewUserStores creates all per-user stores from the directory paths.
func NewUserStores(dirs UserDirs, userID string) (*UserStores, error) {
	stateStore, err := store.NewFS(dirs.State)
	if err != nil {
		return nil, fmt.Errorf("create state store: %w", err)
	}

	sessionStore, err := store.NewFS(dirs.Sessions)
	if err != nil {
		return nil, fmt.Errorf("create session store: %w", err)
	}

	secretStore, err := secret.Resolve(userID, dirs.Secrets, os.Getenv(secret.MasterKeyEnv))
	if err != nil {
		return nil, fmt.Errorf("create secret store: %w", err)
	}

	runtimeState := channel.NewRuntimeStateStore(stateStore)

	return &UserStores{
		State:        stateStore,
		Session:      sessionStore,
		Secret:       secretStore,
		RuntimeState: runtimeState,
	}, nil
}
