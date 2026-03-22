package logbuffer

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
)

// rotateThresholdBytes is the maximum log file size before rotation.
// At typical tclaw log volumes, 20MB covers many weeks of history.
const rotateThresholdBytes = 20 * 1024 * 1024 // 20MB

// RotateIfNeeded renames path to path+".old" if it exceeds rotateThresholdBytes,
// making room for a fresh log file. The .old file is overwritten if it already exists.
// Does nothing if the file does not exist or is below the threshold.
func RotateIfNeeded(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat log file: %w", err)
	}
	if info.Size() < rotateThresholdBytes {
		return nil
	}
	if err := os.Rename(path, path+".old"); err != nil {
		return fmt.Errorf("rotate log file: %w", err)
	}
	return nil
}

// ReadTailLines reads the last maxLines non-empty lines from path. If the
// current file has fewer than maxLines, older lines are prepended from
// path+".old" (the rotated backup) if it exists.
// Returns nil without error if path does not exist.
func ReadTailLines(path string, maxLines int) ([]string, error) {
	current, err := readAllLines(path)
	if err != nil {
		return nil, err
	}

	// If the current file doesn't fill the buffer, pull older lines from the
	// rotated backup as well.
	if len(current) < maxLines {
		old, err := readAllLines(path + ".old")
		if err != nil {
			return nil, err
		}
		if len(old) > 0 {
			current = append(old, current...)
		}
	}

	if len(current) > maxLines {
		current = current[len(current)-maxLines:]
	}
	return current, nil
}

// OpenLogFile opens path for appending, creating the file and any parent
// directories if they don't exist.
func OpenLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	return f, nil
}

// readAllLines reads all non-empty lines from path.
// Returns nil without error if path does not exist.
func readAllLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return lines, nil
}
