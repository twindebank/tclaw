package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"tclaw/internal/libraries/secret"
)

const flyApp = "tclaw"

const deployUsage = `Usage: tclaw deploy [command]

Commands:
  (no subcommand)    Build locally and deploy to Fly.io
  secrets            Push keychain secrets to Fly.io
  suspend            Spin down the Fly.io deployment (scale to 0)
  resume             Spin up the Fly.io deployment (scale to 1)
  status             Show Fly.io app status
  logs               Show recent Fly.io app logs (same as tclaw logs)
`

func runDeploy() {
	subcommand := ""
	if len(os.Args) >= 3 {
		subcommand = os.Args[2]
	}

	switch subcommand {
	case "", "app":
		deployApp()
	case "secrets":
		deploySecrets()
	case "suspend":
		fmt.Println("→ suspending fly.io deployment (scaling to 0)...")
		run("fly", "scale", "count", "0", "-a", flyApp, "--yes")
	case "resume":
		fmt.Println("→ resuming fly.io deployment (scaling to 1)...")
		run("fly", "scale", "count", "1", "-a", flyApp, "--yes")
	case "status":
		run("fly", "status", "-a", flyApp)
	case "logs":
		doLogs(os.Args[3:])
	case "--help", "-h", "help":
		fmt.Print(deployUsage)
	default:
		fmt.Fprintf(os.Stderr, "unknown deploy command: %q\n\n", subcommand)
		fmt.Fprint(os.Stderr, deployUsage)
		os.Exit(1)
	}
}

func deployApp() {
	fmt.Println("→ deploying to fly.io (local build)...")

	// Use the user's Docker socket path if available.
	dockerHost := os.Getenv("DOCKER_HOST")
	if dockerHost == "" {
		home, _ := os.UserHomeDir()
		dockerHost = "unix://" + home + "/.docker/run/docker.sock"
	}

	// Pass the git commit as a build arg since .git is excluded from the Docker context.
	commit, _ := exec.Command("git", "rev-parse", "--short", "HEAD").Output()

	cmd := exec.Command("fly", "deploy", "--local-only", "-a", flyApp,
		"--build-arg", "COMMIT="+strings.TrimSpace(string(commit)))
	cmd.Env = append(os.Environ(), "DOCKER_HOST="+dockerHost)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: deploy failed: %v\n", err)
		os.Exit(1)
	}
}

// secretRefPattern matches ${secret:NAME} references in config files.
var secretRefPattern = regexp.MustCompile(`\$\{secret:([^}]+)\}`)

func deploySecrets() {
	configPath := "tclaw.yaml"
	if len(os.Args) >= 4 {
		configPath = os.Args[3]
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading config: %v\n", err)
		os.Exit(1)
	}

	// Find all unique ${secret:NAME} references.
	matches := secretRefPattern.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		fmt.Println("no ${secret:...} references found in config")
		return
	}

	seen := make(map[string]bool)
	var names []string
	for _, m := range matches {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	if !secret.KeychainAvailable() {
		fmt.Fprintln(os.Stderr, "error: OS keychain not available")
		os.Exit(1)
	}
	store := secret.NewKeychainStore(configNamespace)

	// Build fly secrets set args: NAME=VALUE pairs.
	var args []string
	args = append(args, "secrets", "set")
	for _, name := range names {
		val, err := store.Get(ctx, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %q from keychain: %v\n", name, err)
			os.Exit(1)
		}
		if val == "" {
			fmt.Fprintf(os.Stderr, "error: secret %q not found in keychain\n", name)
			os.Exit(1)
		}
		args = append(args, fmt.Sprintf("%s=%s", name, val))
		fmt.Printf("  ✓ %s (from keychain)\n", name)
	}
	args = append(args, "-a", flyApp)

	fmt.Printf("\npushing %d secrets to fly app %q...\n", len(names), flyApp)
	flyCmd := exec.CommandContext(ctx, "fly", args...)
	flyCmd.Stdout = os.Stdout
	flyCmd.Stderr = os.Stderr
	if err := flyCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: fly secrets set failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("done")
}
