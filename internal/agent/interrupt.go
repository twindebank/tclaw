package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// interruptEscalateAfter is how long an interrupted CLI gets to end its turn before it is asked
// to exit outright. The process is killed once exec's WaitDelay runs out after that.
const interruptEscalateAfter = 4 * time.Second

// cliWaitDelay bounds how long a cancelled CLI may take to exit before it is killed.
const cliWaitDelay = 8 * time.Second

// interruptCLIParams identifies the process tclaw started for a turn.
type interruptCLIParams struct {
	// PID is the process tclaw started: the CLI itself, or bwrap wrapping it.
	PID int

	Sandboxed bool

	// ProcRoot is the proc filesystem to walk when sandboxed.
	ProcRoot string
}

// interruptCLI sends the CLI SIGINT, which ends the turn cleanly, then SIGTERM if it has not
// exited in time. Inside the sandbox the CLI is signalled directly, because bwrap does not
// forward signals and dies on SIGINT, taking the CLI down with it mid-turn.
func interruptCLI(p interruptCLIParams) error {
	target := p.PID
	if p.Sandboxed {
		pid, err := sandboxedCommandPID(p.ProcRoot, p.PID)
		if err != nil {
			return fmt.Errorf("find the CLI inside the sandbox: %w", err)
		}
		target = pid
	}

	if err := syscall.Kill(target, syscall.SIGINT); err != nil {
		return fmt.Errorf("interrupt pid %d: %w", target, err)
	}
	time.AfterFunc(interruptEscalateAfter, func() {
		err := syscall.Kill(target, syscall.SIGTERM)
		switch {
		case err == syscall.ESRCH:
			// Already exited, which is the usual case.
		case err != nil:
			slog.Warn("failed to send SIGTERM to interrupted CLI", "pid", target, "err", err)
		default:
			slog.Warn("CLI did not exit after SIGINT, sent SIGTERM", "pid", target)
		}
	})
	return nil
}

// sandboxedCommandPID returns the first process below bwrapPID that is not bwrap itself. With a
// private PID namespace, bwrap runs a second bwrap as the namespace's init, which starts the command.
func sandboxedCommandPID(procRoot string, bwrapPID int) (int, error) {
	children, err := childrenByParent(procRoot)
	if err != nil {
		return 0, err
	}

	queue := []int{bwrapPID}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, pid := range children[parent] {
			comm, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "comm"))
			if err != nil {
				// The process exited between listing and reading.
				continue
			}
			if strings.TrimSpace(string(comm)) != "bwrap" {
				return pid, nil
			}
			queue = append(queue, pid)
		}
	}
	return 0, fmt.Errorf("no process found below bwrap pid %d", bwrapPID)
}

// childrenByParent maps each parent pid to its children, read from every /proc/<pid>/stat.
func childrenByParent(procRoot string) (map[int][]int, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", procRoot, err)
	}
	children := make(map[int][]int)
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
		ppid, err := parentPID(string(stat))
		if err != nil {
			return nil, fmt.Errorf("pid %d: %w", pid, err)
		}
		children[ppid] = append(children[ppid], pid)
	}
	return children, nil
}

// parentPID reads the ppid field from a /proc/<pid>/stat line. The command name before it is in
// parentheses and may itself contain spaces or parentheses, so fields are counted from the last ")".
func parentPID(stat string) (int, error) {
	end := strings.LastIndex(stat, ")")
	if end < 0 {
		return 0, fmt.Errorf("malformed stat %q", stat)
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 2 {
		return 0, fmt.Errorf("malformed stat %q", stat)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, fmt.Errorf("parse ppid in %q: %w", stat, err)
	}
	return ppid, nil
}
