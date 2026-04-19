// Package claudesettings manages the Claude Code settings.json file at
// homeDir/.claude/settings.json. It provides idempotent helpers to add and
// remove entries from the permissions.allow list so that MCP tools registered
// server-side are immediately usable without requiring the user to manually
// approve each call.
package claudesettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

type settingsPermissions struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// AddPermission adds pattern to the permissions.allow list in
// homeDir/.claude/settings.json. Idempotent — no-op if already present.
// The file is created if it does not exist.
func AddPermission(homeDir, pattern string) error {
	return updatePermissions(homeDir, func(perms *settingsPermissions) {
		if !slices.Contains(perms.Allow, pattern) {
			perms.Allow = append(perms.Allow, pattern)
		}
	})
}

// RemovePermission removes pattern from the permissions.allow list.
// No-op if the pattern is not present.
func RemovePermission(homeDir, pattern string) error {
	return updatePermissions(homeDir, func(perms *settingsPermissions) {
		perms.Allow = slices.DeleteFunc(perms.Allow, func(s string) bool {
			return s == pattern
		})
	})
}

// updatePermissions reads settings.json, applies mutate to the permissions
// block, and writes it back atomically. All other top-level keys are preserved.
func updatePermissions(homeDir string, mutate func(*settingsPermissions)) error {
	path := filepath.Join(homeDir, ".claude", "settings.json")

	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read settings.json: %w", err)
	}

	// Unmarshal preserving all top-level keys so we don't clobber any fields
	// written by the Claude CLI or other tools.
	top := make(map[string]json.RawMessage)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &top); err != nil {
			return fmt.Errorf("parse settings.json: %w", err)
		}
	}

	var perms settingsPermissions
	if rawPerms, ok := top["permissions"]; ok {
		if err := json.Unmarshal(rawPerms, &perms); err != nil {
			return fmt.Errorf("parse permissions in settings.json: %w", err)
		}
	}

	mutate(&perms)

	rawPerms, err := json.Marshal(perms)
	if err != nil {
		return fmt.Errorf("encode permissions: %w", err)
	}
	top["permissions"] = rawPerms

	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings.json: %w", err)
	}
	out = append(out, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}

	// Write atomically: temp file in the same dir, then rename.
	tmp, err := os.CreateTemp(dir, "settings-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp settings file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp settings file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp settings file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("replace settings.json: %w", err)
	}

	return nil
}
