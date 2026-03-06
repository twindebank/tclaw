package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"tclaw/libraries/secret"
)

const usage = `Usage: tclaw-secret <command> <name> [value]

Commands:
  set <name> <value>   Store a secret in the OS keychain
  get <name>           Retrieve a secret from the OS keychain
  delete <name>        Remove a secret from the OS keychain

Secrets stored here are resolved by ${secret:NAME} references in tclaw.yaml.
`

// Keychain namespace for config-level secrets (matches config/config.go).
const configNamespace = "_config"

func main() {
	if len(os.Args) < 3 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	cmd := os.Args[1]
	name := os.Args[2]

	if !secret.KeychainAvailable() {
		fmt.Fprintln(os.Stderr, "error: OS keychain not available")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store := secret.NewKeychainStore(configNamespace)

	switch cmd {
	case "set":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "error: missing value argument")
			fmt.Fprint(os.Stderr, usage)
			os.Exit(1)
		}
		value := os.Args[3]
		if err := store.Set(ctx, name, value); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("stored %q in keychain\n", name)

	case "get":
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

	case "delete":
		if err := store.Delete(ctx, name); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("deleted %q from keychain\n", name)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}
