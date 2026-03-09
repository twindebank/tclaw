package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"syscall"

	"tclaw/libraries/secret"
)

const usage = `Usage: tclaw-secret <command> [args]

Commands:
  set <name> <value>              Store a secret in the OS keychain
  get <name>                      Retrieve a secret from the OS keychain
  delete <name>                   Remove a secret from the OS keychain
  deploy-secrets <config> [app]   Push all ${secret:NAME} refs from a config
                                  file to Fly.io. Reads values from keychain.
                                  Default app: tclaw

Secrets stored here are resolved by ${secret:NAME} references in tclaw.yaml.
`

// Keychain namespace for config-level secrets (matches config/config.go).
const configNamespace = "_config"

// Matches ${secret:NAME} references in config files.
var secretRefPattern = regexp.MustCompile(`\$\{secret:([^}]+)\}`)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	cmd := os.Args[1]

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "set":
		runSet(ctx)
	case "get":
		runGet(ctx)
	case "delete":
		runDelete(ctx)
	case "deploy-secrets":
		runDeploySecrets(ctx)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}

func runSet(ctx context.Context) {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "error: usage: tclaw-secret set <name> <value>")
		os.Exit(1)
	}
	name, value := os.Args[2], os.Args[3]

	store := requireKeychain()
	if err := store.Set(ctx, name, value); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("stored %q in keychain\n", name)
}

func runGet(ctx context.Context) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "error: usage: tclaw-secret get <name>")
		os.Exit(1)
	}
	name := os.Args[2]

	store := requireKeychain()
	val, err := store.Get(ctx, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if val == "" {
		fmt.Fprintf(os.Stderr, "secret %q not found\n", name)
		os.Exit(1)
	}
	fmt.Print(val)
}

func runDelete(ctx context.Context) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "error: usage: tclaw-secret delete <name>")
		os.Exit(1)
	}
	name := os.Args[2]

	store := requireKeychain()
	if err := store.Delete(ctx, name); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("deleted %q from keychain\n", name)
}

// runDeploySecrets scans a config file for ${secret:NAME} references, reads
// each value from the OS keychain, and pushes them all to Fly.io in one call.
func runDeploySecrets(ctx context.Context) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "error: usage: tclaw-secret deploy-secrets <config> [app]")
		os.Exit(1)
	}
	configPath := os.Args[2]

	app := "tclaw"
	if len(os.Args) >= 4 {
		app = os.Args[3]
	}

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

	store := requireKeychain()

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
	args = append(args, "-a", app)

	fmt.Printf("\npushing %d secrets to fly app %q...\n", len(names), app)
	flyCmd := exec.CommandContext(ctx, "fly", args...)
	flyCmd.Stdout = os.Stdout
	flyCmd.Stderr = os.Stderr
	if err := flyCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: fly secrets set failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("done")
}

func requireKeychain() *secret.KeychainStore {
	if !secret.KeychainAvailable() {
		fmt.Fprintln(os.Stderr, "error: OS keychain not available")
		os.Exit(1)
	}
	return secret.NewKeychainStore(configNamespace)
}
