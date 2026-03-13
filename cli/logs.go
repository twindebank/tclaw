package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const logsUsage = `Usage: tclaw logs [options]

Fetch a snapshot of recent Fly.io app logs (not a stream).

Options:
  -n N     Number of lines to show (default: 100)
  -f       Follow (stream) mode — like fly logs
`

func runLogs() {
	// Parse args manually to keep it simple.
	follow := false
	count := "100"
	args := os.Args[2:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "--follow":
			follow = true
		case "-n":
			if i+1 < len(args) {
				count = args[i+1]
				i++
			}
		case "--help", "-h":
			fmt.Print(logsUsage)
			return
		}
	}

	if follow {
		// Stream mode — pass through to fly logs.
		run("fly", "logs", "-a", flyApp)
		return
	}

	// Snapshot mode: capture logs for a few seconds and display them.
	// fly logs streams forever, so we run it briefly and grab what we get.
	cmd := exec.Command("fly", "logs", "-a", flyApp)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error starting fly logs: %v\n", err)
		os.Exit(1)
	}

	// Read lines until we have enough or the buffer drains.
	// fly logs delivers the recent buffer immediately, then streams new ones.
	// We use a short deadline via the process: read what's available, then kill.
	var lines []string
	maxLines := 0
	fmt.Sscanf(count, "%d", &maxLines)
	if maxLines <= 0 {
		maxLines = 100
	}

	buf := make([]byte, 0, 256*1024)
	tmp := make([]byte, 32*1024)
	// Read for up to ~2 seconds to capture the initial buffer dump.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, readErr := stdout.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if readErr != nil {
				return
			}
		}
	}()

	// Wait briefly for the initial buffer, then kill.
	select {
	case <-done:
		// stdout closed (fly logs exited on its own — rare)
	case <-time.After(2 * time.Second):
		// Got enough time for the initial buffer dump.
	}
	cmd.Process.Kill()
	cmd.Wait()

	// Parse and trim to the requested number of lines, showing most recent.
	raw := string(buf)
	allLines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(allLines) > maxLines {
		lines = allLines[len(allLines)-maxLines:]
	} else {
		lines = allLines
	}

	// Strip ANSI escape codes for cleaner output.
	for _, line := range lines {
		fmt.Println(stripANSI(line))
	}
}

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip the CSI sequence: ESC [ ... <final byte>
			j := i + 2
			for j < len(s) && s[j] >= 0x30 && s[j] <= 0x3F {
				j++ // parameter bytes
			}
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2F {
				j++ // intermediate bytes
			}
			if j < len(s) {
				j++ // final byte
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

