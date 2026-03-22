package logbuffer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRotateIfNeeded_NoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tclaw.log")

	// Should be a no-op when the file doesn't exist.
	require.NoError(t, RotateIfNeeded(path))
}

func TestRotateIfNeeded_SmallFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tclaw.log")
	require.NoError(t, os.WriteFile(path, []byte("small log\n"), 0o644))

	require.NoError(t, RotateIfNeeded(path))

	// Original file should still be there — not rotated.
	_, err := os.Stat(path)
	require.NoError(t, err)
	_, err = os.Stat(path + ".old")
	require.True(t, os.IsNotExist(err))
}

func TestRotateIfNeeded_LargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tclaw.log")

	// Write a file just above the rotation threshold.
	data := strings.Repeat("x", rotateThresholdBytes+1)
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))

	require.NoError(t, RotateIfNeeded(path))

	// Original should be gone, .old should exist.
	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(path + ".old")
	require.NoError(t, err)
}

func TestRotateIfNeeded_OverwritesExistingOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tclaw.log")

	data := strings.Repeat("x", rotateThresholdBytes+1)
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
	require.NoError(t, os.WriteFile(path+".old", []byte("old data"), 0o644))

	require.NoError(t, RotateIfNeeded(path))

	// .old should now contain the rotated data (not "old data").
	got, err := os.ReadFile(path + ".old")
	require.NoError(t, err)
	require.Equal(t, data, string(got))
}

func TestReadTailLines_NoFile(t *testing.T) {
	dir := t.TempDir()
	lines, err := ReadTailLines(filepath.Join(dir, "tclaw.log"), 100)
	require.NoError(t, err)
	require.Empty(t, lines)
}

func TestReadTailLines_FewLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tclaw.log")
	require.NoError(t, os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644))

	lines, err := ReadTailLines(path, 100)
	require.NoError(t, err)
	require.Equal(t, []string{"line1", "line2", "line3"}, lines)
}

func TestReadTailLines_CapsAtMaxLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tclaw.log")

	var sb strings.Builder
	for i := 0; i < 10; i++ {
		sb.WriteString("line\n")
	}
	require.NoError(t, os.WriteFile(path, []byte(sb.String()), 0o644))

	lines, err := ReadTailLines(path, 3)
	require.NoError(t, err)
	require.Len(t, lines, 3)
}

func TestReadTailLines_FallsBackToOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tclaw.log")

	// .old has older lines, current has newer lines.
	require.NoError(t, os.WriteFile(path+".old", []byte("old1\nold2\n"), 0o644))
	require.NoError(t, os.WriteFile(path, []byte("new1\nnew2\n"), 0o644))

	lines, err := ReadTailLines(path, 100)
	require.NoError(t, err)
	require.Equal(t, []string{"old1", "old2", "new1", "new2"}, lines)
}

func TestReadTailLines_OldFileNotNeededWhenCurrentSufficient(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tclaw.log")

	require.NoError(t, os.WriteFile(path+".old", []byte("should-not-appear\n"), 0o644))
	require.NoError(t, os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644))

	// maxLines=2: current file has 3 lines, so it's sufficient and .old is not consulted.
	lines, err := ReadTailLines(path, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"line2", "line3"}, lines)
}

func TestOpenLogFile_CreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "tclaw.log")

	f, err := OpenLogFile(path)
	require.NoError(t, err)
	f.Close()

	_, err = os.Stat(path)
	require.NoError(t, err)
}

func TestBuffer_Load(t *testing.T) {
	buf := New(10)
	buf.Load([]string{
		`time=2024-01-01T00:00:00Z level=INFO msg="historical" user=alice`,
		`time=2024-01-01T00:00:01Z level=INFO msg="also historical" user=alice`,
	})

	lines := buf.Query(QueryParams{})
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "historical")
	require.Contains(t, lines[1], "also historical")
}

func TestBuffer_LoadThenWrite(t *testing.T) {
	buf := New(10)
	buf.Load([]string{`time=2024-01-01T00:00:00Z level=INFO msg="old"`})
	buf.Write([]byte("time=2024-01-01T00:00:01Z level=INFO msg=\"new\"\n"))

	lines := buf.Query(QueryParams{})
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "old")
	require.Contains(t, lines[1], "new")
}

func TestBuffer_LoadRespectsCapacity(t *testing.T) {
	buf := New(3)
	buf.Load([]string{"line1", "line2", "line3", "line4", "line5"})

	lines := buf.Query(QueryParams{})
	require.Len(t, lines, 3)
	require.Equal(t, "line3", lines[0])
	require.Equal(t, "line4", lines[1])
	require.Equal(t, "line5", lines[2])
}
