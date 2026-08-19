package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/hooks"
	"tclaw/internal/user"
)

func TestSeedUserMemory(t *testing.T) {
	t.Run("creates from scratch", func(t *testing.T) {
		base := t.TempDir()
		memoryDir := filepath.Join(base, "memory")
		homeDir := filepath.Join(base, "home")

		seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

		data, err := os.ReadFile(filepath.Join(memoryDir, "CLAUDE.md"))
		require.NoError(t, err)
		require.NotEmpty(t, data)

		target, err := os.Readlink(filepath.Join(homeDir, ".claude", "CLAUDE.md"))
		require.NoError(t, err)
		require.Equal(t, filepath.Join("..", "..", "memory", "CLAUDE.md"), target)
	})

	t.Run("idempotent does not overwrite", func(t *testing.T) {
		base := t.TempDir()
		memoryDir := filepath.Join(base, "memory")
		homeDir := filepath.Join(base, "home")

		seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

		claudePath := filepath.Join(memoryDir, "CLAUDE.md")
		require.NoError(t, os.WriteFile(claudePath, []byte("custom content"), 0o600))

		seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

		data, err := os.ReadFile(claudePath)
		require.NoError(t, err)
		require.Equal(t, "custom content", string(data))
	})

	t.Run("recreates missing files", func(t *testing.T) {
		base := t.TempDir()
		memoryDir := filepath.Join(base, "memory")
		homeDir := filepath.Join(base, "home")

		seedUserMemory(user.ID("testuser"), memoryDir, homeDir)
		require.NoError(t, os.RemoveAll(memoryDir))
		require.NoError(t, os.RemoveAll(filepath.Join(homeDir, ".claude")))

		seedUserMemory(user.ID("testuser"), memoryDir, homeDir)

		_, err := os.Stat(filepath.Join(memoryDir, "CLAUDE.md"))
		require.NoError(t, err, "CLAUDE.md should be re-created")
		_, err = os.Lstat(filepath.Join(homeDir, ".claude", "CLAUDE.md"))
		require.NoError(t, err, "symlink should be re-created")
	})
}

func TestSeedHooks(t *testing.T) {
	t.Run("keeps every other setting when it registers the hooks", func(t *testing.T) {
		home := t.TempDir()
		claudeDir := filepath.Join(home, ".claude")
		require.NoError(t, os.MkdirAll(claudeDir, 0o700))
		settingsPath := filepath.Join(claudeDir, "settings.json")
		require.NoError(t, os.WriteFile(settingsPath, []byte(`{"model":"opus","hooks":{"Stop":[]}}`), 0o600))

		// The binary is found on PATH, which is how the router locates it in the
		// image. Without it the router leaves settings.json alone, so the test
		// supplies one.
		t.Setenv("PATH", fakeHookBinary(t)+string(os.PathListSeparator)+os.Getenv("PATH"))

		seedHooks("theo", settingsPath)

		raw, err := os.ReadFile(settingsPath)
		require.NoError(t, err)
		var settings map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &settings))

		require.JSONEq(t, `"opus"`, string(settings["model"]), "an unrelated setting was lost")

		var events map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		require.NoError(t, json.Unmarshal(settings["hooks"], &events))
		registered := 0
		for _, groups := range events {
			for _, group := range groups {
				registered += len(group.Hooks)
			}
		}
		require.Equal(t, len(hooks.Manifest), registered, "every hook in the manifest must be registered")
	})

	t.Run("leaves a settings file it cannot parse alone", func(t *testing.T) {
		home := t.TempDir()
		claudeDir := filepath.Join(home, ".claude")
		require.NoError(t, os.MkdirAll(claudeDir, 0o700))
		settingsPath := filepath.Join(claudeDir, "settings.json")
		require.NoError(t, os.WriteFile(settingsPath, []byte("not json"), 0o600))
		t.Setenv("PATH", fakeHookBinary(t)+string(os.PathListSeparator)+os.Getenv("PATH"))

		seedHooks("theo", settingsPath)

		raw, err := os.ReadFile(settingsPath)
		require.NoError(t, err)
		require.Equal(t, "not json", string(raw), "a file that cannot be read must not be overwritten")
	})
}

// fakeHookBinary puts an executable named like the hook binary on PATH and
// returns its directory. What it does is irrelevant: registration only records
// where it is.
func fakeHookBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, hooks.BinaryName)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return dir
}
