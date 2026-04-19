package claudesettings_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/claudesettings"
)

func TestAddPermission(t *testing.T) {
	t.Run("creates settings.json with permission when file does not exist", func(t *testing.T) {
		homeDir := t.TempDir()

		require.NoError(t, claudesettings.AddPermission(homeDir, "mcp__home-assistant__*"))

		allow := readAllow(t, homeDir)
		require.Equal(t, []string{"mcp__home-assistant__*"}, allow)
	})

	t.Run("appends to existing allow list", func(t *testing.T) {
		homeDir := t.TempDir()
		writeSettings(t, homeDir, `{"permissions":{"allow":["mcp__linear__*"]}}`)

		require.NoError(t, claudesettings.AddPermission(homeDir, "mcp__home-assistant__*"))

		allow := readAllow(t, homeDir)
		require.Contains(t, allow, "mcp__linear__*")
		require.Contains(t, allow, "mcp__home-assistant__*")
	})

	t.Run("is idempotent — does not duplicate existing pattern", func(t *testing.T) {
		homeDir := t.TempDir()

		require.NoError(t, claudesettings.AddPermission(homeDir, "mcp__ha__*"))
		require.NoError(t, claudesettings.AddPermission(homeDir, "mcp__ha__*"))

		allow := readAllow(t, homeDir)
		require.Len(t, allow, 1)
	})

	t.Run("preserves other top-level keys in settings.json", func(t *testing.T) {
		homeDir := t.TempDir()
		writeSettings(t, homeDir, `{"env":{"SOME_VAR":"value"},"permissions":{"allow":[]}}`)

		require.NoError(t, claudesettings.AddPermission(homeDir, "mcp__test__*"))

		raw, err := os.ReadFile(filepath.Join(homeDir, ".claude", "settings.json"))
		require.NoError(t, err)

		var top map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &top))
		require.Contains(t, top, "env", "env key must be preserved")
	})

	t.Run("works on empty JSON object", func(t *testing.T) {
		homeDir := t.TempDir()
		writeSettings(t, homeDir, `{}`)

		require.NoError(t, claudesettings.AddPermission(homeDir, "mcp__test__*"))

		allow := readAllow(t, homeDir)
		require.Equal(t, []string{"mcp__test__*"}, allow)
	})
}

func TestRemovePermission(t *testing.T) {
	t.Run("removes the specified pattern", func(t *testing.T) {
		homeDir := t.TempDir()
		writeSettings(t, homeDir, `{"permissions":{"allow":["mcp__ha__*","mcp__linear__*"]}}`)

		require.NoError(t, claudesettings.RemovePermission(homeDir, "mcp__ha__*"))

		allow := readAllow(t, homeDir)
		require.Equal(t, []string{"mcp__linear__*"}, allow)
	})

	t.Run("is a no-op if pattern not present", func(t *testing.T) {
		homeDir := t.TempDir()
		writeSettings(t, homeDir, `{"permissions":{"allow":["mcp__linear__*"]}}`)

		require.NoError(t, claudesettings.RemovePermission(homeDir, "mcp__other__*"))

		allow := readAllow(t, homeDir)
		require.Equal(t, []string{"mcp__linear__*"}, allow)
	})

	t.Run("is a no-op if file does not exist", func(t *testing.T) {
		homeDir := t.TempDir()

		require.NoError(t, claudesettings.RemovePermission(homeDir, "mcp__ha__*"))
	})
}

// --- helpers ---

func writeSettings(t *testing.T, homeDir, content string) {
	t.Helper()
	dir := filepath.Join(homeDir, ".claude")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o600))
}

func readAllow(t *testing.T, homeDir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(homeDir, ".claude", "settings.json"))
	require.NoError(t, err)

	var top struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	require.NoError(t, json.Unmarshal(raw, &top))
	return top.Permissions.Allow
}
