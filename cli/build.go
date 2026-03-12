package cli

import (
	"fmt"
	"os"
	"os/exec"
)

func runBuild() {
	fmt.Println("→ building...")
	run("go", "build", "-o", "bin/tclaw", ".")
	runInDir("cmd/chat", "go", "build", "-o", "../../bin/tclaw-chat", ".")
	fmt.Println("✓ bin/tclaw  bin/tclaw-chat")
}

func runInstall() {
	fmt.Println("→ installing tclaw...")
	run("go", "install", ".")
	fmt.Println("→ installing tclaw-chat...")
	runInDir("cmd/chat", "go", "install", ".")
	fmt.Println("✓ installed tclaw, tclaw-chat")
}

func runTidy() {
	fmt.Println("→ tidying dependencies...")
	run("go", "mod", "tidy")
	runInDir("cmd/chat", "go", "mod", "tidy")
	fmt.Println("✓ done")
}

// run executes a command with inherited stdio and exits on failure.
func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// runInDir executes a command in a subdirectory.
func runInDir(dir, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
