package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/creack/pty/v2"

	"tclaw/internal/agent"
	"tclaw/internal/libraries/secret"
)

const initBanner = `
  tclaw — multi-user Claude Code host

  tclaw spawns isolated Claude Code CLI subprocesses as your personal AI
  assistant, adding persistent memory, multi-channel communication,
  scheduling, and MCP tool extensibility on top.
`

const configTemplate = `# tclaw configuration — see tclaw.example.yaml for all options.

local:
  base_dir: /tmp/tclaw

  users:
    - id: {{.UserID}}
      {{- if .APIKey}}
      api_key: ${secret:ANTHROPIC_API_KEY}
      {{- end}}
      model: {{.Model}}
      permission_mode: dontAsk
      tool_groups:
        - core_tools
        - all_builtins
        - channel_messaging
        - channel_management
        - scheduling
        - dev_workflow
        - repo_monitoring
        - gsuite_read
        - gsuite_write
        - personal_services
        - connections
        - telegram_client
        - notifications
        - onboarding
        - secret_form

      channels:
        - type: socket
          name: main
          description: Primary desktop workstation
`

type configData struct {
	UserID string
	Model  string
	APIKey bool
}

func runInit() {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", "tclaw.yaml", "output config file path")
	force := fs.Bool("force", false, "overwrite existing config file")
	fs.Parse(os.Args[2:])

	fmt.Print(initBanner)

	// Check prerequisites.
	fmt.Println("  Checking prerequisites...")
	allOK := true
	allOK = checkPrereq("go", "Go", "https://go.dev/dl/") && allOK
	allOK = checkPrereq("node", "Node.js", "https://nodejs.org/") && allOK
	allOK = checkPrereq("claude", "claude CLI", "npm install -g @anthropic-ai/claude-code") && allOK
	fmt.Println()

	if !allOK {
		fmt.Println("  Install the missing prerequisites and try again.")
		fmt.Println()
		os.Exit(1)
	}

	// Check existing config.
	if !*force {
		if _, err := os.Stat(*configPath); err == nil {
			fmt.Fprintf(os.Stderr, "  %s already exists — use --force to overwrite.\n\n", *configPath)
			os.Exit(1)
		}
	}

	scanner := bufio.NewScanner(os.Stdin)

	// Prompt for user ID.
	userID := prompt(scanner, "  User ID", "default")

	// Prompt for model.
	model := prompt(scanner, "  Model", "claude-sonnet-4-6")

	// Prompt for auth method.
	fmt.Println()
	fmt.Println("  Authentication — tclaw uses Claude Code as its brain and needs")
	fmt.Println("  access to the Claude API. Choose one:")
	fmt.Println()
	fmt.Println("    1 — Claude Pro/Teams (OAuth, opens browser)")
	fmt.Println("    2 — Anthropic API key (paste sk-ant-...)")
	fmt.Println("    3 — Skip (set up later on first message)")
	fmt.Println()

	authChoice := prompt(scanner, "  Choice", "1")

	usedAPIKey := false
	switch strings.TrimSpace(authChoice) {
	case "1":
		runOAuthSetup()
	case "2":
		usedAPIKey = runAPIKeySetup(scanner)
	case "3":
		fmt.Println("  Skipped — the agent will walk you through auth on first message.")
	default:
		fmt.Println("  Skipped — the agent will walk you through auth on first message.")
	}

	// Generate config.
	fmt.Println()
	data := configData{
		UserID: userID,
		Model:  model,
		APIKey: usedAPIKey,
	}

	tmpl, err := template.New("config").Parse(configTemplate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  error: failed to parse config template: %v\n", err)
		os.Exit(1)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		fmt.Fprintf(os.Stderr, "  error: failed to generate config: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*configPath, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "  error: failed to write %s: %v\n", *configPath, err)
		os.Exit(1)
	}

	fmt.Printf("  ✓ Created %s\n", *configPath)
	fmt.Println()
	fmt.Println("  Next steps:")
	fmt.Println("    1. tclaw serve         (start the server)")
	fmt.Println("    2. tclaw chat          (connect the chat client, in a second terminal)")
	fmt.Println()
}

// prompt prints a prompt with a default value and reads user input.
// Returns the default if the user enters nothing.
func prompt(scanner *bufio.Scanner, label string, defaultVal string) string {
	fmt.Printf("%s [%s]: ", label, defaultVal)
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text != "" {
			return text
		}
	}
	return defaultVal
}

// checkPrereq checks if a binary is on PATH, prints status, returns success.
func checkPrereq(binary string, name string, installHint string) bool {
	path, err := exec.LookPath(binary)
	if err != nil {
		fmt.Printf("    ✗ %s not found\n", name)
		fmt.Printf("      Install: %s\n", installHint)
		return false
	}

	// Try to get version for display.
	version := getVersion(path)
	if version != "" {
		fmt.Printf("    ✓ %s %s\n", name, version)
	} else {
		fmt.Printf("    ✓ %s\n", name)
	}
	return true
}

// getVersion runs `binary --version` and extracts a short version string.
func getVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "--version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Take the first line, trim common prefixes.
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])

	// For commands like "go version go1.26.2 darwin/arm64", extract the version part.
	if strings.HasPrefix(line, "go version ") {
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			return parts[2]
		}
	}

	return line
}

// runOAuthSetup runs `claude setup-token` interactively and stores the result.
func runOAuthSetup() {
	fmt.Println()
	fmt.Println("  Opening browser for Claude OAuth...")
	fmt.Println("  Complete the login in your browser, then return here.")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "setup-token")

	// Use a pty so the CLI opens the browser for interactive OAuth.
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 512})
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Failed to start setup-token: %v\n", err)
		fmt.Println("    You can authenticate later on first message.")
		return
	}
	defer ptmx.Close()

	var stdout bytes.Buffer
	if _, err := io.Copy(&stdout, ptmx); err != nil {
		// EIO is expected when the child exits and the pty closes.
		var errno syscall.Errno
		if !errors.As(err, &errno) || errno != syscall.EIO {
			fmt.Fprintf(os.Stderr, "  warning: error reading setup-token output: %v\n", err)
		}
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			fmt.Println("  ✗ Timed out waiting for OAuth — you can authenticate later on first message.")
			return
		}
		fmt.Fprintf(os.Stderr, "  ✗ setup-token failed: %v\n", err)
		fmt.Println("    You can authenticate later on first message.")
		return
	}

	token := agent.ExtractSetupToken(stdout.String())
	if token == "" {
		fmt.Println("  ✗ Could not extract token from output.")
		fmt.Println("    You can authenticate later on first message.")
		return
	}

	// Store the setup token in the keychain so config resolution finds it.
	if secret.KeychainAvailable() {
		store := secret.NewKeychainStore(configNamespace)
		if err := store.Set(context.Background(), "CLAUDE_SETUP_TOKEN", token); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ Failed to save token to keychain: %v\n", err)
			return
		}
	}

	fmt.Println("  ✅ Authenticated. Token saved.")
}

// runAPIKeySetup prompts for an API key and stores it in the keychain.
// Returns true if a key was successfully stored.
func runAPIKeySetup(scanner *bufio.Scanner) bool {
	fmt.Println()
	fmt.Print("  API key: ")
	if !scanner.Scan() {
		return false
	}
	key := strings.TrimSpace(scanner.Text())

	if !agent.ValidAPIKey(key) {
		fmt.Println("  ✗ Invalid key — must start with sk-ant- and be at least 50 characters.")
		fmt.Println("    You can set it later: tclaw secret set ANTHROPIC_API_KEY <key>")
		return false
	}

	if !secret.KeychainAvailable() {
		fmt.Println("  ✗ OS keychain not available — cannot store API key.")
		return false
	}

	store := secret.NewKeychainStore(configNamespace)
	if err := store.Set(context.Background(), "ANTHROPIC_API_KEY", key); err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Failed to save key to keychain: %v\n", err)
		return false
	}

	fmt.Println("  ✅ API key saved to keychain.")
	return true
}
