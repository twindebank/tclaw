package logbuffer

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuffer_BasicCapture(t *testing.T) {
	buf := New(10)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:00Z level=INFO msg="hello world" user=alice`)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:01Z level=ERROR msg="something broke" user=alice`)

	lines := buf.Query(QueryParams{})
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "hello world")
	require.Contains(t, lines[1], "something broke")
}

func TestBuffer_RingEviction(t *testing.T) {
	buf := New(3)
	for i := 0; i < 5; i++ {
		fmt.Fprintf(buf, "time=2024-01-01T00:00:00Z level=INFO msg=\"line %d\" user=alice\n", i)
	}

	lines := buf.Query(QueryParams{})
	require.Len(t, lines, 3)
	require.Contains(t, lines[0], "line 2")
	require.Contains(t, lines[1], "line 3")
	require.Contains(t, lines[2], "line 4")
}

func TestBuffer_UserIsolation(t *testing.T) {
	buf := New(100)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:00Z level=INFO msg="alice thing" user=alice`)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:01Z level=INFO msg="bob thing" user=bob`)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:02Z level=INFO msg="alice again" user=alice`)

	aliceLines := buf.Query(QueryParams{UserID: "alice"})
	require.Len(t, aliceLines, 2)
	require.Contains(t, aliceLines[0], "alice thing")
	require.Contains(t, aliceLines[1], "alice again")

	bobLines := buf.Query(QueryParams{UserID: "bob"})
	require.Len(t, bobLines, 1)
	require.Contains(t, bobLines[0], "bob thing")
}

func TestBuffer_UserIsolation_PrefixSafety(t *testing.T) {
	// "the" should NOT match "theo"
	buf := New(100)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:00Z level=INFO msg="theo's log" user=theo`)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:01Z level=INFO msg="the's log" user=the`)

	theoLines := buf.Query(QueryParams{UserID: "theo"})
	require.Len(t, theoLines, 1)
	require.Contains(t, theoLines[0], "theo's log")

	theLines := buf.Query(QueryParams{UserID: "the"})
	require.Len(t, theLines, 1)
	require.Contains(t, theLines[0], "the's log")
}

func TestBuffer_IncludeSystem(t *testing.T) {
	buf := New(100)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:00Z level=INFO msg="server started"`)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:01Z level=INFO msg="alice thing" user=alice`)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:02Z level=INFO msg="shutting down"`)

	// Without include_system: only user-tagged lines.
	lines := buf.Query(QueryParams{UserID: "alice"})
	require.Len(t, lines, 1)

	// With include_system: user lines + system lines.
	lines = buf.Query(QueryParams{UserID: "alice", IncludeSystem: true})
	require.Len(t, lines, 3)
}

func TestBuffer_LevelFilter(t *testing.T) {
	buf := New(100)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:00Z level=DEBUG msg="debug msg"`)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:01Z level=INFO msg="info msg"`)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:02Z level=WARN msg="warn msg"`)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:03Z level=ERROR msg="error msg"`)

	lines := buf.Query(QueryParams{Level: "WARN"})
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "warn msg")
	require.Contains(t, lines[1], "error msg")
}

func TestBuffer_ContainsFilter(t *testing.T) {
	buf := New(100)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:00Z level=INFO msg="starting agent"`)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:01Z level=INFO msg="mcp config ready"`)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:02Z level=ERROR msg="agent error"`)

	lines := buf.Query(QueryParams{Contains: "agent"})
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "starting agent")
	require.Contains(t, lines[1], "agent error")

	// Case-insensitive.
	lines = buf.Query(QueryParams{Contains: "AGENT"})
	require.Len(t, lines, 2)
}

func TestBuffer_MaxLines(t *testing.T) {
	buf := New(100)
	for i := 0; i < 10; i++ {
		fmt.Fprintf(buf, "time=2024-01-01T00:00:00Z level=INFO msg=\"line %d\"\n", i)
	}

	lines := buf.Query(QueryParams{MaxLines: 3})
	require.Len(t, lines, 3)
	require.Contains(t, lines[0], "line 7")
	require.Contains(t, lines[1], "line 8")
	require.Contains(t, lines[2], "line 9")
}

func TestBuffer_PartialWrite(t *testing.T) {
	// Simulate writes that split a line across multiple Write calls.
	buf := New(10)
	buf.Write([]byte(`time=2024-01-01T00:00:00Z level=INFO msg=`))
	buf.Write([]byte("\"hello\"\n"))

	lines := buf.Query(QueryParams{})
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "hello")
}

func TestBuffer_EmptyQuery(t *testing.T) {
	buf := New(10)
	lines := buf.Query(QueryParams{})
	require.Empty(t, lines)
}

func TestBuffer_NoUserFilter_ShowsAll(t *testing.T) {
	buf := New(100)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:00Z level=INFO msg="system log"`)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:01Z level=INFO msg="user log" user=alice`)

	// No user filter: show everything.
	lines := buf.Query(QueryParams{})
	require.Len(t, lines, 2)
}

func TestBuffer_SinceFilter(t *testing.T) {
	buf := New(100)
	fmt.Fprintln(buf, `time=2024-01-01T00:00:00Z level=INFO msg="old line"`)
	fmt.Fprintln(buf, `time=2024-01-02T00:00:00Z level=INFO msg="new line"`)

	cutoff := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	lines := buf.Query(QueryParams{Since: cutoff})
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "new line")
}

func TestBuffer_SinceFilter_IncludesExactBoundary(t *testing.T) {
	buf := New(100)
	ts := "2024-06-15T10:00:00Z"
	fmt.Fprintf(buf, "time=%s level=INFO msg=\"boundary line\"\n", ts)

	cutoff := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
	lines := buf.Query(QueryParams{Since: cutoff})
	require.Len(t, lines, 1)
}

func TestBuffer_SinceFilter_WorksForLoadedLines(t *testing.T) {
	// Lines loaded from file have zero entry.time — since filter must parse
	// the timestamp from the log text itself.
	buf := New(100)
	buf.Load([]string{
		`time=2024-01-01T00:00:00Z level=INFO msg="old loaded"`,
		`time=2024-01-02T00:00:00Z level=INFO msg="new loaded"`,
	})

	cutoff := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	lines := buf.Query(QueryParams{Since: cutoff})
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "new loaded")
}

func TestExtractTime(t *testing.T) {
	t.Run("valid RFC3339", func(t *testing.T) {
		line := `time=2024-06-15T10:30:00.123Z level=INFO msg="hello"`
		got, ok := extractTime(line)
		require.True(t, ok)
		require.Equal(t, 2024, got.Year())
		require.Equal(t, time.June, got.Month())
		require.Equal(t, 15, got.Day())
	})

	t.Run("no time field", func(t *testing.T) {
		_, ok := extractTime(`level=INFO msg="no time"`)
		require.False(t, ok)
	})
}
