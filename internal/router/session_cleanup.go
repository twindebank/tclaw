package router

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/store"
)

const sessionRetention = 7 * 24 * time.Hour

// cleanupStaleSessions removes CLI session files and session store records
// that haven't been active within the retention period. Active sessions
// (the current session for each channel) are never deleted regardless of age.
func cleanupStaleSessions(ctx context.Context, sessionStore *channel.SessionStore, sessionBackingStore store.Store, sessionsDir string, homeDir string) {
	// The CLI stores session files under {Home}/.claude/projects/{project-key}/
	// where project-key is the CWD path with slashes replaced by dashes.
	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		slog.Warn("session cleanup: failed to read projects dir", "err", err)
		return
	}

	activeSessionIDs := collectActiveSessionIDs(ctx, sessionsDir, sessionStore)
	cutoff := time.Now().Add(-sessionRetention)

	var deletedFiles, deletedDirs int
	var freedBytes int64

	for _, projectEntry := range entries {
		if !projectEntry.IsDir() {
			continue
		}
		projectPath := filepath.Join(projectsDir, projectEntry.Name())
		sessionFiles, readErr := os.ReadDir(projectPath)
		if readErr != nil {
			slog.Warn("session cleanup: failed to read project dir", "dir", projectPath, "err", readErr)
			continue
		}

		for _, f := range sessionFiles {
			if !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}

			sessionID := strings.TrimSuffix(f.Name(), ".jsonl")
			if activeSessionIDs[sessionID] {
				continue
			}

			info, statErr := f.Info()
			if statErr != nil {
				continue
			}
			if info.ModTime().After(cutoff) {
				continue
			}

			jsonlPath := filepath.Join(projectPath, f.Name())
			freedBytes += info.Size()
			if rmErr := os.Remove(jsonlPath); rmErr != nil {
				slog.Warn("session cleanup: failed to delete session file", "path", jsonlPath, "err", rmErr)
				continue
			}
			deletedFiles++

			// Delete the corresponding data directory (subagents, tool-results).
			dataDir := filepath.Join(projectPath, sessionID)
			if dirInfo, dirErr := os.Stat(dataDir); dirErr == nil && dirInfo.IsDir() {
				dirSize := dirSize(dataDir)
				if rmErr := os.RemoveAll(dataDir); rmErr != nil {
					slog.Warn("session cleanup: failed to delete session dir", "path", dataDir, "err", rmErr)
				} else {
					freedBytes += dirSize
					deletedDirs++
				}
			}
		}
	}

	prunedRecords := pruneSessionStoreRecords(ctx, sessionStore, sessionBackingStore, sessionsDir, activeSessionIDs, cutoff)

	if deletedFiles > 0 || prunedRecords > 0 {
		slog.Info("session cleanup complete",
			"deleted_files", deletedFiles,
			"deleted_dirs", deletedDirs,
			"pruned_records", prunedRecords,
			"freed_mb", float64(freedBytes)/(1024*1024),
		)
	}
}

// collectActiveSessionIDs returns the set of session IDs that are currently
// assigned to a channel (i.e. SessionStore.Current returns them).
func collectActiveSessionIDs(ctx context.Context, sessionsDir string, sessionStore *channel.SessionStore) map[string]bool {
	active := make(map[string]bool)

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		slog.Warn("session cleanup: failed to read sessions dir", "err", err)
		return active
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		channelKey := entry.Name()
		sid, loadErr := sessionStore.Current(ctx, channelKey)
		if loadErr != nil {
			slog.Warn("session cleanup: failed to load session", "channel_key", channelKey, "err", loadErr)
			continue
		}
		if sid != "" {
			active[sid] = true
		}
	}

	return active
}

// pruneSessionStoreRecords removes old session records from the session store
// for each channel. Active sessions and records newer than cutoff are kept.
func pruneSessionStoreRecords(ctx context.Context, sessionStore *channel.SessionStore, backingStore store.Store, sessionsDir string, activeSessionIDs map[string]bool, cutoff time.Time) int {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return 0
	}

	var totalPruned int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		channelKey := entry.Name()

		records, loadErr := sessionStore.List(ctx, channelKey)
		if loadErr != nil {
			continue
		}

		var kept []channel.SessionRecord
		for _, r := range records {
			if activeSessionIDs[r.SessionID] || r.StartedAt.After(cutoff) {
				kept = append(kept, r)
			}
		}

		pruned := len(records) - len(kept)
		if pruned == 0 {
			continue
		}

		data, marshalErr := json.Marshal(kept)
		if marshalErr != nil {
			slog.Warn("session cleanup: failed to marshal pruned records", "channel_key", channelKey, "err", marshalErr)
			continue
		}
		if setErr := backingStore.Set(ctx, channelKey, data); setErr != nil {
			slog.Warn("session cleanup: failed to save pruned records", "channel_key", channelKey, "err", setErr)
			continue
		}
		totalPruned += pruned
	}

	return totalPruned
}

// dirSize returns the total size of all files in a directory tree.
func dirSize(path string) int64 {
	var size int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}
