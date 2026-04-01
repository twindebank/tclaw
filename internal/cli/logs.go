package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const logsUsage = `Usage: tclaw logs [options]

Fetch recent logs from the persisted log file on the Fly volume.

Options:
  -n N     Number of lines to show (default: 100)
  -f       Follow (stream) mode — tails the log file in real time
`

// logFilePath is where tclaw persists logs on the Fly volume.
const logFilePath = "/data/tclaw/logs/tclaw.log"

func runLogs() {
	doLogs(os.Args[2:])
}

func doLogs(args []string) {
	follow := false
	count := "100"
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
		// Stream mode — tail -f the persistent log file on the Fly VM.
		run("fly", "ssh", "console", "-a", flyApp, "-C", "tail -f "+logFilePath)
		return
	}

	// Snapshot mode: read the last N lines from the persistent log file.
	remoteCmd := fmt.Sprintf("tail -n %s %s", count, logFilePath)
	cmd := exec.Command("fly", "ssh", "console", "-a", flyApp, "-C", remoteCmd)
	cmd.Stderr = os.Stderr

	output, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading logs: %v\n", err)
		os.Exit(1)
	}

	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	for _, line := range lines {
		fmt.Println(line)
	}
}
