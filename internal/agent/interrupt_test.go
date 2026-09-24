package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSandboxedCommandPID(t *testing.T) {
	t.Run("finds the command below bwrap and its namespace init", func(t *testing.T) {
		proc := t.TempDir()
		writeProc(t, proc, 100, 1, "tclaw")
		writeProc(t, proc, 200, 100, "bwrap")
		writeProc(t, proc, 201, 200, "bwrap")
		writeProc(t, proc, 202, 201, "claude")
		writeProc(t, proc, 300, 202, "bash")

		pid, err := sandboxedCommandPID(proc, 200)
		require.NoError(t, err)
		require.Equal(t, 202, pid, "the CLI, not bwrap and not the CLI's own children")
	})

	t.Run("reads the parent past a command name holding spaces and parentheses", func(t *testing.T) {
		proc := t.TempDir()
		writeProc(t, proc, 200, 1, "bwrap")
		writeProc(t, proc, 202, 200, "odd (name) here")

		pid, err := sandboxedCommandPID(proc, 200)
		require.NoError(t, err)
		require.Equal(t, 202, pid)
	})

	t.Run("fails when nothing runs below bwrap", func(t *testing.T) {
		proc := t.TempDir()
		writeProc(t, proc, 200, 1, "bwrap")

		_, err := sandboxedCommandPID(proc, 200)
		require.Error(t, err)
		require.Equal(t, "no process found below bwrap pid 200", err.Error())
	})
}

// --- helpers ---

// writeProc fakes the two /proc files sandboxedCommandPID reads for one process.
func writeProc(t *testing.T, root string, pid, ppid int, comm string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	stat := strconv.Itoa(pid) + " (" + comm + ") S " + strconv.Itoa(ppid) + " 1 1 0 -1"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644))
}
