package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func runChat() {
	// Try to find the tclaw-chat binary: first next to this binary, then on PATH.
	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), "tclaw-chat")
		if _, err := os.Stat(candidate); err == nil {
			run(candidate, os.Args[2:]...)
			return
		}
	}

	if path, err := exec.LookPath("tclaw-chat"); err == nil {
		run(path, os.Args[2:]...)
		return
	}

	// Fallback: build and run from source.
	// cmd/chat is a separate Go module, so we must run from inside its directory.
	runChatFromSource()
}

func runChatFromSource() {
	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: tclaw-chat not found and can't locate source: %v\n", err)
		fmt.Fprintln(os.Stderr, "  install it with: make install  (or: cd cmd/chat && go install .)")
		os.Exit(1)
	}

	chatDir := filepath.Join(repoRoot, "cmd", "chat")
	args := append([]string{"run", "."}, os.Args[2:]...)

	cmd := exec.Command("go", args...)
	cmd.Dir = chatDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Pass through the config path so chat can find the socket.
	configPath := os.Getenv("TCLAW_CONFIG")
	if configPath == "" {
		configPath = filepath.Join(repoRoot, "tclaw.yaml")
	}
	cmd.Env = append(os.Environ(), "TCLAW_CONFIG="+configPath)

	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "error running chat: %v\n", err)
		os.Exit(1)
	}
}

// findRepoRoot walks up from the current working directory looking for go.mod.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// Verify it's the tclaw repo by checking cmd/chat exists.
			if _, err := os.Stat(filepath.Join(dir, "cmd", "chat")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find tclaw repo root")
		}
		dir = parent
	}
}
