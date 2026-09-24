package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSandboxedCommandPID(t *testing.T) {
	t.Run("finds the command below bwrap and its namespace init", func(t *testing.T) {
		proc := t.TempDir()
		writeProc(t, proc, fakeProc{PID: 100, ParentPID: 1, Comm: "tclaw", StartTime: 10})
		writeProc(t, proc, fakeProc{PID: 200, ParentPID: 100, Comm: "bwrap", StartTime: 20})
		writeProc(t, proc, fakeProc{PID: 201, ParentPID: 200, Comm: "bwrap", StartTime: 21})
		writeProc(t, proc, fakeProc{PID: 202, ParentPID: 201, Comm: "claude", StartTime: 22})
		writeProc(t, proc, fakeProc{PID: 300, ParentPID: 202, Comm: "bash", StartTime: 30})

		pid, err := sandboxedCommandPID(proc, 200)
		require.NoError(t, err)
		require.Equal(t, 202, pid, "the CLI, not bwrap and not the CLI's own children")
	})

	t.Run("picks the command over a process it orphaned, whatever the pids", func(t *testing.T) {
		proc := t.TempDir()
		writeProc(t, proc, fakeProc{PID: 200, ParentPID: 1, Comm: "bwrap", StartTime: 20})
		writeProc(t, proc, fakeProc{PID: 201, ParentPID: 200, Comm: "bwrap", StartTime: 21})
		writeProc(t, proc, fakeProc{PID: 9999, ParentPID: 201, Comm: "claude", StartTime: 22})
		// A background job the CLI started and left behind is re-parented to the
		// namespace init. Its pid sorts first as text.
		writeProc(t, proc, fakeProc{PID: 10000, ParentPID: 201, Comm: "sleep", StartTime: 50})

		pid, err := sandboxedCommandPID(proc, 200)
		require.NoError(t, err)
		require.Equal(t, 9999, pid)
	})

	t.Run("reads a command name holding spaces and parentheses", func(t *testing.T) {
		proc := t.TempDir()
		writeProc(t, proc, fakeProc{PID: 200, ParentPID: 1, Comm: "bwrap", StartTime: 20})
		writeProc(t, proc, fakeProc{PID: 201, ParentPID: 200, Comm: "bwrap", StartTime: 21})
		writeProc(t, proc, fakeProc{PID: 202, ParentPID: 201, Comm: "odd (name) here", StartTime: 22})

		pid, err := sandboxedCommandPID(proc, 200)
		require.NoError(t, err)
		require.Equal(t, 202, pid)
	})

	t.Run("fails before bwrap has started the command", func(t *testing.T) {
		proc := t.TempDir()
		writeProc(t, proc, fakeProc{PID: 200, ParentPID: 1, Comm: "bwrap", StartTime: 20})
		writeProc(t, proc, fakeProc{PID: 201, ParentPID: 200, Comm: "bwrap", StartTime: 21})

		_, err := sandboxedCommandPID(proc, 200)
		require.Error(t, err)
		require.Equal(t, "no command found below bwrap pid 200", err.Error())
	})
}

// --- helpers ---

type fakeProc struct {
	PID       int
	ParentPID int
	Comm      string
	StartTime uint64
}

// writeProc fakes /proc/<pid>/stat in the kernel's layout: pid, (comm), then fields 3 to 22.
func writeProc(t *testing.T, root string, p fakeProc) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(p.PID))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	stat := fmt.Sprintf("%d (%s) S %d 1 1 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 %d 0 0", p.PID, p.Comm, p.ParentPID, p.StartTime)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644))
}
