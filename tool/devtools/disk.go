package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tclaw/mcp"
)

// diskSpaceWarningThreshold is the minimum available space before we warn.
// The gotd/td vendor directory alone is ~500MB per worktree.
const diskSpaceWarningMB = 200

// checkDiskSpace returns an error if available disk space is below the
// warning threshold. Used by dev_start to prevent worktree creation when
// disk is nearly full.
func checkDiskSpace(baseDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "df", "-m", baseDir).Output()
	if err != nil {
		// Can't check — don't block the operation.
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return nil
	}

	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return nil
	}

	var availMB int
	if _, err := fmt.Sscanf(fields[3], "%d", &availMB); err != nil {
		return nil
	}

	if availMB < diskSpaceWarningMB {
		return fmt.Errorf("low disk space: only %dMB available on %s (need at least %dMB for a worktree) — run dev_disk to see what's using space, consider cleaning up old worktrees with dev_end or dev_cancel",
			availMB, fields[5], diskSpaceWarningMB)
	}

	return nil
}

const ToolDisk = "dev_disk"

func devDiskDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolDisk,
		Description: "Show disk usage for the tclaw data volume. Returns volume-level " +
			"stats (total, used, available, percent) and a per-directory breakdown of " +
			"the user's data directory (memory, worktrees, repos, secrets, etc.).",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func devDiskHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		baseDir := filepath.Dir(deps.UserDir)

		type volumeInfo struct {
			Filesystem string `json:"filesystem"`
			Size       string `json:"size"`
			Used       string `json:"used"`
			Available  string `json:"available"`
			UsePct     string `json:"use_percent"`
			MountedOn  string `json:"mounted_on"`
		}

		type dirEntry struct {
			Path string `json:"path"`
			Size string `json:"size"`
		}

		type diskResult struct {
			Volume    *volumeInfo `json:"volume,omitempty"`
			UserDir   string      `json:"user_dir"`
			UserTotal string      `json:"user_total,omitempty"`
			Breakdown []dirEntry  `json:"breakdown,omitempty"`
			Errors    []string    `json:"errors,omitempty"`
		}

		result := diskResult{UserDir: deps.UserDir}

		// Volume-level stats from df.
		dfOut, err := exec.CommandContext(ctx, "df", "-h", baseDir).Output()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("df failed: %v", err))
		} else {
			lines := strings.Split(strings.TrimSpace(string(dfOut)), "\n")
			if len(lines) >= 2 {
				fields := strings.Fields(lines[len(lines)-1])
				if len(fields) >= 6 {
					result.Volume = &volumeInfo{
						Filesystem: fields[0],
						Size:       fields[1],
						Used:       fields[2],
						Available:  fields[3],
						UsePct:     fields[4],
						MountedOn:  fields[5],
					}
				}
			}
		}

		// Per-directory breakdown of user dir.
		duOut, err := exec.CommandContext(ctx, "du", "-sh", deps.UserDir).Output()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("du total failed: %v", err))
		} else {
			fields := strings.Fields(strings.TrimSpace(string(duOut)))
			if len(fields) >= 1 {
				result.UserTotal = fields[0]
			}
		}

		// Breakdown by subdirectory (depth 1).
		duDetailOut, err := exec.CommandContext(ctx, "du", "-sh", deps.UserDir+"/*").Output()
		if err != nil {
			// du with glob might not work — fall back to listing subdirs individually.
			duDetailOut, err = exec.CommandContext(ctx, "sh", "-c", "du -sh "+deps.UserDir+"/* 2>/dev/null | sort -rh").Output()
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("du breakdown failed: %v", err))
			}
		}
		if len(duDetailOut) > 0 {
			for _, line := range strings.Split(strings.TrimSpace(string(duDetailOut)), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					// Show relative path from user dir for readability.
					relPath := fields[1]
					if rel, relErr := filepath.Rel(deps.UserDir, fields[1]); relErr == nil {
						relPath = rel
					}
					result.Breakdown = append(result.Breakdown, dirEntry{
						Path: relPath,
						Size: fields[0],
					})
				}
			}
		}

		return json.Marshal(result)
	}
}
