package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tclaw/internal/config"
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

// deploySecrets is the `tclaw deploy secrets` entry point. Failures exit
// non-zero; callers that need to sequence work around the sync use
// syncBootSecrets directly.
func deploySecrets() {
	configPath := "tclaw.yaml"
	if len(os.Args) >= 4 {
		configPath = os.Args[3]
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if _, err := syncBootSecrets(ctx, configPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("done")
}

// syncBootSecrets pushes the ${boot:NAME} secrets declared under the prod
// environment from the OS keychain to the Fly app.
//
// Only the prod environment is scanned. The environments reuse secret names
// for different values — the local dev bot and the production bot are both
// TELEGRAM_BOT_TOKEN — so scanning the whole file would push a dev credential
// to production, and would fail on any machine holding only one environment's
// secrets.
//
// Every value is resolved before anything is sent, so a missing secret aborts
// with nothing pushed rather than leaving Fly holding a partial set.
//
// Returns how many secrets were actually sent, so a caller can tell whether
// the app was restarted as a result.
func syncBootSecrets(ctx context.Context, configPath string) (int, error) {
	names, err := config.BootSecretRefs(configPath, config.EnvProd)
	if err != nil {
		return 0, fmt.Errorf("read boot secret refs: %w", err)
	}
	if len(names) == 0 {
		fmt.Println("no ${boot:...} references found in the prod config")
		return 0, nil
	}

	if !secret.KeychainAvailable() {
		return 0, fmt.Errorf("OS keychain not available")
	}
	store := secret.NewKeychainStore(configNamespace)

	// Resolve everything up front — see the partial-push note above.
	values := make(map[string]string, len(names))
	var missing []string
	for _, name := range names {
		val, getErr := store.Get(ctx, name)
		if getErr != nil {
			return 0, fmt.Errorf("read %q from keychain: %w", name, getErr)
		}
		if val == "" {
			missing = append(missing, name)
			continue
		}
		values[name] = val
	}
	// A secret absent locally but already set on Fly is fine: it was pushed
	// from another machine and is unchanged. Demanding a local copy of every
	// prod secret would make this command unusable from any machine that does
	// not hold the full set — which is most of them. Only a secret missing in
	// both places is fatal, because nothing would ever supply it.
	if len(missing) > 0 {
		onFly, listErr := flySecretNames(ctx)
		if listErr != nil {
			return 0, fmt.Errorf("secrets not in keychain (%s) and could not check which are already on Fly: %w",
				strings.Join(missing, ", "), listErr)
		}
		var absent []string
		for _, name := range missing {
			if !onFly[name] {
				absent = append(absent, name)
				continue
			}
			fmt.Printf("  - %s (not in keychain; already set on Fly, leaving as-is)\n", name)
		}
		if len(absent) > 0 {
			return 0, fmt.Errorf("secrets missing from both the keychain and Fly: %s\n  set each with: tclaw secret set <NAME> <value>",
				strings.Join(absent, ", "))
		}
	}

	if len(values) == 0 {
		fmt.Println("  all prod secrets already set on Fly, nothing to push")
		return 0, nil
	}

	args := []string{"secrets", "set"}
	pushed := 0
	for _, name := range names {
		val, ok := values[name]
		if !ok {
			continue
		}
		args = append(args, fmt.Sprintf("%s=%s", name, val))
		fmt.Printf("  ✓ %s (from keychain)\n", name)
		pushed++
	}
	args = append(args, "-a", flyApp)

	fmt.Printf("\npushing %d secrets to fly app %q...\n", pushed, flyApp)
	flyCmd := exec.CommandContext(ctx, "fly", args...)
	flyCmd.Stdout = os.Stdout
	flyCmd.Stderr = os.Stderr
	if err := flyCmd.Run(); err != nil {
		return 0, fmt.Errorf("fly secrets set failed: %w", err)
	}
	return pushed, nil
}

// waitForStartedMachine blocks until the app has a started machine, or the
// timeout expires.
//
// Setting a secret restarts the app, and for a brief window afterwards there is
// no started VM — long enough that a `fly ssh` immediately after fails with
// "app has no started VMs". Staging the secrets instead is not an option:
// applying them needs a full `fly deploy`, which belongs to CI, and neither
// `fly secrets deploy` nor a machine restart clears a staged secret.
func waitForStartedMachine(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		out, err := exec.CommandContext(ctx, "fly", "machine", "list", "-a", flyApp, "--json").Output()
		if err == nil {
			var machines []struct {
				State string `json:"state"`
			}
			if json.Unmarshal(out, &machines) == nil {
				for _, m := range machines {
					if m.State == "started" {
						return nil
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no started machine on %q after %s", flyApp, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// flySecretNames returns the secret names already set on the Fly app. Only
// names are available from Fly — values are write-only — which is all this
// needs to tell "already provisioned elsewhere" from "nothing will ever set
// this".
func flySecretNames(ctx context.Context) (map[string]bool, error) {
	out, err := exec.CommandContext(ctx, "fly", "secrets", "list", "-a", flyApp, "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("fly secrets list: %w", err)
	}
	var entries []struct {
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("parse fly secrets list: %w", err)
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name] = true
	}
	return names, nil
}
