package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"tclaw/libraries/secret"
)

const secretUsage = `Usage: tclaw secret <command> [args]

Commands:
  set <name> <value>              Store a secret in the OS keychain
  get <name>                      Retrieve a secret from the OS keychain
  delete <name>                   Remove a secret from the OS keychain

Secrets stored here are resolved by ${secret:NAME} references in tclaw.yaml.
`

// Keychain namespace for config-level secrets (matches config/config.go).
const configNamespace = "_config"

func runSecret() {
	if len(os.Args) < 3 {
		fmt.Fprint(os.Stderr, secretUsage)
		os.Exit(1)
	}

	cmd := os.Args[2]

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "set":
		secretSet(ctx)
	case "get":
		secretGet(ctx)
	case "delete":
		secretDelete(ctx)
	case "--help", "-h", "help":
		fmt.Print(secretUsage)
	default:
		fmt.Fprintf(os.Stderr, "unknown secret command: %q\n\n", cmd)
		fmt.Fprint(os.Stderr, secretUsage)
		os.Exit(1)
	}
}

func secretSet(ctx context.Context) {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "error: usage: tclaw secret set <name> <value>")
		os.Exit(1)
	}
	name, value := os.Args[3], os.Args[4]

	store := requireKeychain()
	if err := store.Set(ctx, name, value); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("stored %q in keychain\n", name)
}

func secretGet(ctx context.Context) {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "error: usage: tclaw secret get <name>")
		os.Exit(1)
	}
	name := os.Args[3]

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

func secretDelete(ctx context.Context) {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "error: usage: tclaw secret delete <name>")
		os.Exit(1)
	}
	name := os.Args[3]

	store := requireKeychain()
	if err := store.Delete(ctx, name); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("deleted %q from keychain\n", name)
}

func requireKeychain() *secret.KeychainStore {
	if !secret.KeychainAvailable() {
		fmt.Fprintln(os.Stderr, "error: OS keychain not available")
		os.Exit(1)
	}
	return secret.NewKeychainStore(configNamespace)
}
