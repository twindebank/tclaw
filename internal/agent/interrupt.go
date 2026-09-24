package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// cliWaitDelay is how long an interrupted CLI gets to end its turn and exit before it is killed.
const cliWaitDelay = 5 * time.Second

// interruptCLI sends the CLI SIGINT, which ends the turn cleanly. Inside the sandbox the CLI is
// signalled directly, because bwrap does not forward signals and dies on SIGINT, killing the CLI.
func interruptCLI(pid int, sandboxed bool) error {
	target := pid
	if sandboxed {
		found, err := sandboxedCommandPID("/proc", pid)
		if err != nil {
			return fmt.Errorf("find the CLI inside the sandbox: %w", err)
		}
		target = found
	}
	if err := syscall.Kill(target, syscall.SIGINT); err != nil {
		return fmt.Errorf("interrupt pid %d: %w", target, err)
	}
	return nil
}

// sandboxedCommandPID returns the command bwrap is running. With a private PID namespace bwrap
// runs a second bwrap as the namespace's init, which starts the command; anything the command
// orphans is re-parented to that init too, so the command is its earliest-started child.
func sandboxedCommandPID(procRoot string, bwrapPID int) (int, error) {
	procs, err := readProcs(procRoot)
	if err != nil {
		return 0, err
	}

	for _, init := range procs {
		if init.ParentPID != bwrapPID || init.Comm != "bwrap" {
			continue
		}
		var command *procInfo
		for i, p := range procs {
			if p.ParentPID == init.PID && (command == nil || p.StartTime < command.StartTime) {
				command = &procs[i]
			}
		}
		if command != nil {
			return command.PID, nil
		}
	}
	return 0, fmt.Errorf("no command found below bwrap pid %d", bwrapPID)
}

// procInfo is what sandboxedCommandPID needs to know about one process.
type procInfo struct {
	PID       int
	ParentPID int
	Comm      string

	// StartTime is in clock ticks since boot, so only its order matters.
	StartTime uint64
}

// readProcs reads every process's /proc/<pid>/stat. A process that exits while being read is skipped.
func readProcs(procRoot string) ([]procInfo, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", procRoot, err)
	}
	var procs []procInfo
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			// Not a process directory, e.g. /proc/self or /proc/meminfo.
			continue
		}
		stat, err := os.ReadFile(filepath.Join(procRoot, entry.Name(), "stat"))
		if err != nil {
			// The process exited between listing and reading.
			continue
		}
		info, err := parseStat(pid, string(stat))
		if err != nil {
			return nil, err
		}
		procs = append(procs, info)
	}
	return procs, nil
}

// parseStat reads the command name, parent and start time from a /proc/<pid>/stat line. The name
// is in parentheses and may itself hold spaces or parentheses, so fields count from the last ")".
func parseStat(pid int, stat string) (procInfo, error) {
	open := strings.Index(stat, "(")
	end := strings.LastIndex(stat, ")")
	if open < 0 || end < open {
		return procInfo{}, fmt.Errorf("pid %d: malformed stat %q", pid, stat)
	}
	// Fields after the name start at field 3 (state); ppid is field 4 and starttime field 22.
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 20 {
		return procInfo{}, fmt.Errorf("pid %d: malformed stat %q", pid, stat)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return procInfo{}, fmt.Errorf("pid %d: parse ppid: %w", pid, err)
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return procInfo{}, fmt.Errorf("pid %d: parse starttime: %w", pid, err)
	}
	return procInfo{PID: pid, ParentPID: ppid, Comm: stat[open+1 : end], StartTime: start}, nil
}
