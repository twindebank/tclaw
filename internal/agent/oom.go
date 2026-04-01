package agent

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
)

// ProtectFromOOM lowers the current process's OOM score so the kernel prefers
// killing child processes (the claude CLI) over tclaw itself. Only effective on
// Linux — no-op on other platforms.
func ProtectFromOOM() {
	if runtime.GOOS != "linux" {
		return
	}
	// -999 makes us very unlikely to be OOM-killed (only -1000 is fully exempt,
	// which requires root and could cause system-wide deadlocks).
	if err := os.WriteFile("/proc/self/oom_score_adj", []byte("-999"), 0o644); err != nil {
		slog.Warn("failed to set oom_score_adj for tclaw process", "err", err)
	} else {
		slog.Info("oom protection enabled for tclaw process", "oom_score_adj", -999)
	}
}

// markSubprocessOOMTarget raises the subprocess's OOM score so the kernel kills
// it before tclaw when memory is tight. Called after cmd.Start().
func markSubprocessOOMTarget(cmd *exec.Cmd) {
	if runtime.GOOS != "linux" {
		return
	}
	pid := cmd.Process.Pid
	path := fmt.Sprintf("/proc/%d/oom_score_adj", pid)
	if err := os.WriteFile(path, []byte("1000"), 0o644); err != nil {
		slog.Warn("failed to set oom_score_adj for subprocess", "pid", pid, "err", err)
	}
}
