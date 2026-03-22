package logbuffer

import (
	"strings"
	"sync"
	"time"
)

// Buffer is a thread-safe ring buffer that stores the most recent log lines.
// It implements io.Writer so it can be used as a tee alongside os.Stderr in
// the slog handler.
type Buffer struct {
	mu    sync.Mutex
	lines []entry
	max   int

	// write buffer for accumulating partial lines across Write calls
	partial strings.Builder
}

type entry struct {
	time time.Time
	text string
}

// New creates a ring buffer that retains the most recent maxLines log lines.
func New(maxLines int) *Buffer {
	return &Buffer{
		lines: make([]entry, 0, maxLines),
		max:   maxLines,
	}
}

// Load pre-populates the buffer with existing log lines (e.g. from a persisted
// log file on startup). Lines are appended in order; the ring capacity still
// applies, so only the most recent max lines are retained if len(lines) > max.
func (b *Buffer) Load(lines []string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, line := range lines {
		if line != "" {
			b.appendLocked(entry{text: line})
		}
	}
}

// Write implements io.Writer. slog's TextHandler writes complete log lines
// (terminated with \n) but may batch multiple lines in a single call.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.partial.Write(p)
	raw := b.partial.String()

	// Process all complete lines (terminated by \n).
	for {
		idx := strings.IndexByte(raw, '\n')
		if idx < 0 {
			break
		}
		line := raw[:idx]
		raw = raw[idx+1:]
		if line == "" {
			continue
		}
		b.appendLocked(entry{time: time.Now(), text: line})
	}

	// Keep any trailing partial line for next Write call.
	b.partial.Reset()
	if raw != "" {
		b.partial.WriteString(raw)
	}

	return len(p), nil
}

func (b *Buffer) appendLocked(e entry) {
	if len(b.lines) >= b.max {
		// Shift left by 1 — simple and correct for the expected max (~5000).
		copy(b.lines, b.lines[1:])
		b.lines[len(b.lines)-1] = e
	} else {
		b.lines = append(b.lines, e)
	}
}

// QueryParams controls which log lines are returned.
type QueryParams struct {
	// UserID filters to lines containing user=<id>. Empty means no filter.
	UserID string

	// IncludeSystem includes lines that have no user= field at all (e.g.
	// startup, HTTP server, shutdown). Ignored when UserID is empty.
	IncludeSystem bool

	// Level filters to lines at or above this slog level string
	// ("DEBUG", "INFO", "WARN", "ERROR"). Empty means no filter.
	Level string

	// Contains filters to lines matching this substring (case-insensitive).
	Contains string

	// MaxLines caps the number of returned lines (most recent). 0 = no cap.
	MaxLines int
}

// Query returns log lines matching the given params, most recent last.
func (b *Buffer) Query(p QueryParams) []string {
	b.mu.Lock()
	snapshot := make([]entry, len(b.lines))
	copy(snapshot, b.lines)
	b.mu.Unlock()

	containsLower := strings.ToLower(p.Contains)

	var result []string
	for _, e := range snapshot {
		if !matchesEntry(e.text, p, containsLower) {
			continue
		}
		result = append(result, e.text)
	}

	if p.MaxLines > 0 && len(result) > p.MaxLines {
		result = result[len(result)-p.MaxLines:]
	}
	return result
}

func matchesEntry(line string, p QueryParams, containsLower string) bool {
	// User isolation: if a user ID is specified, only show lines tagged with
	// that user OR (optionally) system lines with no user field at all.
	if p.UserID != "" {
		hasUser := strings.Contains(line, "user=")
		if hasUser {
			if !matchesUser(line, p.UserID) {
				return false
			}
		} else if !p.IncludeSystem {
			return false
		}
	}

	if p.Level != "" && !matchesLevel(line, p.Level) {
		return false
	}

	if containsLower != "" && !strings.Contains(strings.ToLower(line), containsLower) {
		return false
	}

	return true
}

// matchesUser checks for user=<id> in the slog text output. The value is
// either unquoted (user=theo) or quoted (user="theo with spaces"). We match
// exactly to prevent "the" matching "theo".
func matchesUser(line, userID string) bool {
	target := "user=" + userID
	idx := strings.Index(line, target)
	if idx < 0 {
		return false
	}
	// Check that the match ends at a field boundary (space, end of line, or
	// the next slog field). This prevents "user=the" matching "user=theo".
	end := idx + len(target)
	if end >= len(line) {
		return true
	}
	return line[end] == ' ' || line[end] == '\t' || line[end] == '\n'
}

// matchesLevel filters by minimum slog level. Level ordering: DEBUG < INFO < WARN < ERROR.
func matchesLevel(line, minLevel string) bool {
	lineLevel := extractLevel(line)
	return levelOrd(lineLevel) >= levelOrd(minLevel)
}

func extractLevel(line string) string {
	// slog TextHandler format: "... level=INFO ..."
	idx := strings.Index(line, "level=")
	if idx < 0 {
		return ""
	}
	rest := line[idx+6:]
	end := strings.IndexByte(rest, ' ')
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func levelOrd(level string) int {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return 0
	case "INFO":
		return 1
	case "WARN", "WARNING":
		return 2
	case "ERROR":
		return 3
	default:
		return -1
	}
}
