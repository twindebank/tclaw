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
  fly-config         Push local fly.toml to Fly without rebuilding the image
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
	case "fly-config":
		deployFlyConfig()
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

// deployFlyConfig pushes the local fly.toml to Fly without rebuilding the
// Docker image. Useful for changing concurrency limits, health check settings,
// VM size, or other Fly platform config without a full deploy cycle.
//
// It works by redeploying the currently-running image with the updated fly.toml.
// The fly.toml is gitignored so CI deploys don't touch it — this is the only
// way to update Fly platform config.
func deployFlyConfig() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	const localPath = "fly.toml"
	if _, err := os.Stat(localPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s not found — copy fly.example.toml and edit it\n", localPath)
		os.Exit(1)
	}

	// Show what's changing by diffing local against live config.
	fmt.Println("→ fetching live config from Fly...")
	liveCmd := exec.CommandContext(ctx, "fly", "config", "show", "--toml", "-a", flyApp)
	liveData, err := liveCmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error fetching live config: %v\n", err)
		os.Exit(1)
	}

	localData, err := os.ReadFile(localPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", localPath, err)
		os.Exit(1)
	}

	// Show diff (live → local) so additions are green.
	liveTmp, err := os.CreateTemp("", "tclaw-fly-live-*.toml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating temp file: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(liveTmp.Name())
	liveTmp.Write(liveData)
	liveTmp.Close()

	localTmp, err := os.CreateTemp("", "tclaw-fly-local-*.toml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating temp file: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(localTmp.Name())
	localTmp.Write(localData)
	localTmp.Close()

	diffCmd := exec.CommandContext(ctx, "diff", "-u",
		"--label", "live (Fly)",
		"--label", "local (fly.toml)",
		liveTmp.Name(), localTmp.Name())
	diffOut, _ := diffCmd.Output()

	if len(diffOut) == 0 {
		fmt.Println("done: fly.toml matches live config, nothing to deploy")
		return
	}

	fmt.Println("\nconfig diff (live → local):")
	fmt.Println(string(diffOut))

	// Get the currently deployed image so we can redeploy it without rebuilding.
	fmt.Println("→ looking up current image...")
	statusCmd := exec.CommandContext(ctx, "fly", "status", "-a", flyApp)
	statusOut, err := statusCmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error fetching app status: %v\n", err)
		os.Exit(1)
	}

	image := extractImage(string(statusOut))
	if image == "" {
		fmt.Fprintln(os.Stderr, "error: could not determine current image from fly status output")
		os.Exit(1)
	}

	// Redeploy with the same image — only the fly.toml config changes.
	fmt.Printf("→ deploying config update (image: %s)...\n", image)
	deployCmd := exec.CommandContext(ctx, "fly", "deploy", "-a", flyApp,
		"--image", "registry.fly.io/"+flyApp+":"+image)
	deployCmd.Stdin = os.Stdin
	deployCmd.Stdout = os.Stdout
	deployCmd.Stderr = os.Stderr
	if err := deployCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: deploy failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("done: fly.toml config deployed")
}

// extractImage parses the image tag from `fly status` output.
// The output contains a line like: "Image    = tclaw:deployment-01KN73ZWZPWBPJ00C7RWT03ZW4"
func extractImage(statusOutput string) string {
	for _, line := range strings.Split(statusOutput, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Image") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		full := strings.TrimSpace(parts[1])
		if idx := strings.Index(full, ":"); idx >= 0 {
			return full[idx+1:]
		}
	}
	return ""
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
