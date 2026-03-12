package router

import (
	"os"
	"path/filepath"
	"testing"

	"tclaw/agent"
	"tclaw/user"
)

// seedDir creates a directory with the given files. Each file gets "content" as its body.
func seedDir(t *testing.T, dir string, files ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, f := range files {
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir parent of %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

// dirFiles returns all file names in a directory (non-recursive).
func dirFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func setupTestDirs(t *testing.T) (memoryDir, homeDir, sessionsDir, stateDir, secretsDir, runtimeDir string) {
	t.Helper()
	base := t.TempDir()
	memoryDir = filepath.Join(base, "memory")
	homeDir = filepath.Join(base, "home")
	sessionsDir = filepath.Join(base, "sessions")
	stateDir = filepath.Join(base, "state")
	secretsDir = filepath.Join(base, "secrets")
	runtimeDir = filepath.Join(base, "runtime")

	// Seed all directories with test files.
	seedDir(t, memoryDir, "CLAUDE.md", "topic-a.md", "topic-b.md")
	seedDir(t, filepath.Join(homeDir, ".claude"), "settings.json", "projects/session1.jsonl")
	seedDir(t, sessionsDir, "main.sock", "telegram")
	seedDir(t, stateDir, "connections", "schedules", "channels")
	seedDir(t, secretsDir, "anthropic_api_key.enc", "conn_google_work.enc")
	seedDir(t, runtimeDir, "mcp-config.json")

	return
}

func TestResetUser_Memories(t *testing.T) {
	memoryDir, homeDir, sessionsDir, stateDir, secretsDir, runtimeDir := setupTestDirs(t)

	if err := resetUser(agent.ResetMemories, memoryDir, homeDir, sessionsDir, stateDir, secretsDir, runtimeDir); err != nil {
		t.Fatalf("resetUser: %v", err)
	}

	// Memory dir should be empty.
	if files := dirFiles(t, memoryDir); len(files) != 0 {
		t.Errorf("memory dir should be empty, got: %v", files)
	}

	// Everything else should be untouched.
	if files := dirFiles(t, filepath.Join(homeDir, ".claude")); len(files) == 0 {
		t.Error("home/.claude should not be cleared")
	}
	if files := dirFiles(t, sessionsDir); len(files) == 0 {
		t.Error("sessions should not be cleared")
	}
	if files := dirFiles(t, stateDir); len(files) == 0 {
		t.Error("state should not be cleared")
	}
	if files := dirFiles(t, secretsDir); len(files) == 0 {
		t.Error("secrets should not be cleared")
	}
	if files := dirFiles(t, runtimeDir); len(files) == 0 {
		t.Error("runtime should not be cleared")
	}
}

func TestResetUser_Project(t *testing.T) {
	memoryDir, homeDir, sessionsDir, stateDir, secretsDir, runtimeDir := setupTestDirs(t)

	if err := resetUser(agent.ResetProject, memoryDir, homeDir, sessionsDir, stateDir, secretsDir, runtimeDir); err != nil {
		t.Fatalf("resetUser: %v", err)
	}

	// Claude state and sessions should be empty.
	if files := dirFiles(t, filepath.Join(homeDir, ".claude")); len(files) != 0 {
		t.Errorf("home/.claude should be empty, got: %v", files)
	}
	if files := dirFiles(t, sessionsDir); len(files) != 0 {
		t.Errorf("sessions should be empty, got: %v", files)
	}

	// Memory, state, secrets, runtime should be untouched.
	if files := dirFiles(t, memoryDir); len(files) == 0 {
		t.Error("memory should not be cleared")
	}
	if files := dirFiles(t, stateDir); len(files) == 0 {
		t.Error("state should not be cleared")
	}
	if files := dirFiles(t, secretsDir); len(files) == 0 {
		t.Error("secrets should not be cleared")
	}
	if files := dirFiles(t, runtimeDir); len(files) == 0 {
		t.Error("runtime should not be cleared")
	}
}

func TestResetUser_All(t *testing.T) {
	memoryDir, homeDir, sessionsDir, stateDir, secretsDir, runtimeDir := setupTestDirs(t)

	if err := resetUser(agent.ResetAll, memoryDir, homeDir, sessionsDir, stateDir, secretsDir, runtimeDir); err != nil {
		t.Fatalf("resetUser: %v", err)
	}

	// Everything should be empty.
	for _, dir := range []string{
		memoryDir,
		filepath.Join(homeDir, ".claude"),
		sessionsDir,
		stateDir,
		secretsDir,
		runtimeDir,
	} {
		if files := dirFiles(t, dir); len(files) != 0 {
			t.Errorf("%s should be empty, got: %v", dir, files)
		}
	}
}

func TestResetUser_DirectoriesNotRemoved(t *testing.T) {
	memoryDir, homeDir, sessionsDir, stateDir, secretsDir, runtimeDir := setupTestDirs(t)

	if err := resetUser(agent.ResetAll, memoryDir, homeDir, sessionsDir, stateDir, secretsDir, runtimeDir); err != nil {
		t.Fatalf("resetUser: %v", err)
	}

	// The directories themselves should still exist (only contents removed).
	for _, dir := range []string{memoryDir, sessionsDir, stateDir, secretsDir, runtimeDir} {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("directory %s should still exist after reset", dir)
		}
	}
}

func TestResetUser_MissingDirectoryIsNotError(t *testing.T) {
	base := t.TempDir()

	// Pass directories that don't exist — should not error.
	err := resetUser(agent.ResetAll,
		filepath.Join(base, "nope-memory"),
		filepath.Join(base, "nope-home"),
		filepath.Join(base, "nope-sessions"),
		filepath.Join(base, "nope-state"),
		filepath.Join(base, "nope-secrets"),
		filepath.Join(base, "nope-runtime"),
	)
	if err != nil {
		t.Fatalf("expected no error for missing dirs, got: %v", err)
	}
}

func TestClearDirectoryContents(t *testing.T) {
	dir := t.TempDir()
	seedDir(t, dir, "a.txt", "b.txt", "subdir/c.txt")

	if err := clearDirectoryContents(dir); err != nil {
		t.Fatalf("clearDirectoryContents: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected empty dir, got %d entries", len(entries))
	}

	// Directory itself should still exist.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("directory should still exist")
	}
}

func TestClearDirectoryContents_NonExistent(t *testing.T) {
	if err := clearDirectoryContents("/nonexistent/path/12345"); err != nil {
		t.Fatalf("expected nil for nonexistent dir, got: %v", err)
	}
}

func TestSeedUserMemory_CreatesFromScratch(t *testing.T) {
	base := t.TempDir()
	memoryDir := filepath.Join(base, "memory")
	homeDir := filepath.Join(base, "home")

	seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

	// CLAUDE.md should exist in memory dir.
	claudePath := filepath.Join(memoryDir, "CLAUDE.md")
	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("CLAUDE.md not created: %v", err)
	}
	if len(data) == 0 {
		t.Error("CLAUDE.md should have content")
	}

	// Symlink should exist.
	linkPath := filepath.Join(homeDir, ".claude", "CLAUDE.md")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
	if target != filepath.Join("..", "..", "memory", "CLAUDE.md") {
		t.Errorf("unexpected symlink target: %s", target)
	}
}

func TestSeedUserMemory_Idempotent(t *testing.T) {
	base := t.TempDir()
	memoryDir := filepath.Join(base, "memory")
	homeDir := filepath.Join(base, "home")

	// Seed once.
	seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

	// Write custom content to CLAUDE.md.
	claudePath := filepath.Join(memoryDir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte("custom content"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Seed again — should NOT overwrite custom content.
	seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "custom content" {
		t.Errorf("seedUserMemory overwrote existing CLAUDE.md, got: %s", string(data))
	}
}

func TestSeedUserMemory_RecoversAfterReset(t *testing.T) {
	base := t.TempDir()
	memoryDir := filepath.Join(base, "memory")
	homeDir := filepath.Join(base, "home")

	// Seed, then clear (simulating a reset), then seed again.
	seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

	if err := clearDirectoryContents(memoryDir); err != nil {
		t.Fatal(err)
	}
	if err := clearDirectoryContents(filepath.Join(homeDir, ".claude")); err != nil {
		t.Fatal(err)
	}

	// Re-seed should recreate everything.
	seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

	if _, err := os.Stat(filepath.Join(memoryDir, "CLAUDE.md")); err != nil {
		t.Error("CLAUDE.md should be re-created after reset")
	}
	if _, err := os.Lstat(filepath.Join(homeDir, ".claude", "CLAUDE.md")); err != nil {
		t.Error("symlink should be re-created after reset")
	}
}
