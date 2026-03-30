package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

const configUsage = `Usage: tclaw config <command> [flags]

Commands:
  push     Push local config to remote Fly volume
  pull     Pull remote config to local
  diff     Show differences between local and remote config

Flags for push:
  --persist  Also update the TCLAW_YAML GitHub secret for disaster recovery
`

func runConfig() {
	subcommand := ""
	if len(os.Args) >= 3 {
		subcommand = os.Args[2]
	}

	switch subcommand {
	case "push":
		configPush()
	case "pull":
		configPull()
	case "diff":
		configDiff()
	case "--help", "-h", "help", "":
		fmt.Print(configUsage)
	default:
		fmt.Fprintf(os.Stderr, "unknown config command: %q\n\n", subcommand)
		fmt.Fprint(os.Stderr, configUsage)
		os.Exit(1)
	}
}

// remoteConfigPath is where the runtime config lives on the persistent Fly
// volume. Agent mutations (channel create/edit/delete) write here so they
// survive redeploys. See bootstrapConfig() in serve.go for how the seed
// config is copied here on first boot.
const remoteConfigPath = "/data/tclaw.yaml"

// configPush overwrites the remote config on the Fly volume with the local
// one. Optionally also updates the TCLAW_YAML GitHub secret so the config
// survives a volume wipe (disaster recovery).
func configPush() {
	fs := flag.NewFlagSet("config push", flag.ExitOnError)
	persist := fs.Bool("persist", false, "Also update the TCLAW_YAML GitHub secret for disaster recovery")
	fs.Parse(os.Args[3:])

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	localPath := "tclaw.yaml"
	localData, err := os.ReadFile(localPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading local config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("-> pushing local config to remote volume...")
	if err := writeRemoteFile(ctx, remoteConfigPath, localData); err != nil {
		fmt.Fprintf(os.Stderr, "error writing remote config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("-> syncing secrets...")
	deploySecrets()

	if *persist {
		fmt.Println("-> updating TCLAW_YAML GitHub secret...")
		if err := updateGitHubConfigSecret(ctx, localData); err != nil {
			fmt.Fprintf(os.Stderr, "error updating GitHub secret: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Println("done: config pushed")
}

// configPull reads the remote config from the Fly volume and writes it to the
// local tclaw.yaml. Use this to pull agent-created channels back to your local
// config.
func configPull() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Println("-> reading remote config from fly volume...")
	remoteData, err := readRemoteFile(ctx, remoteConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading remote config: %v\n", err)
		fmt.Println("  (if this is first deploy, use 'tclaw config push' to write initial config)")
		os.Exit(1)
	}

	localPath := "tclaw.yaml"
	localData, err := os.ReadFile(localPath)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error reading local config: %v\n", err)
		os.Exit(1)
	}

	if bytes.Equal(localData, remoteData) {
		fmt.Println("done: configs are identical, nothing to pull")
		return
	}

	if err := os.WriteFile(localPath, remoteData, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing local config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("done: config pulled to", localPath)
}

func configDiff() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	localPath := "tclaw.yaml"
	localData, err := os.ReadFile(localPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading local config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("-> reading remote config from fly volume...")
	remoteData, err := readRemoteFile(ctx, remoteConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading remote config: %v\n", err)
		os.Exit(1)
	}

	if bytes.Equal(localData, remoteData) {
		fmt.Println("done: configs are identical")
		return
	}

	localTmp, err := os.CreateTemp("", "tclaw-local-*.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating temp file: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(localTmp.Name())

	remoteTmp, err := os.CreateTemp("", "tclaw-remote-*.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating temp file: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(remoteTmp.Name())

	if _, err := localTmp.Write(localData); err != nil {
		fmt.Fprintf(os.Stderr, "error writing temp file: %v\n", err)
		os.Exit(1)
	}
	localTmp.Close()

	if _, err := remoteTmp.Write(remoteData); err != nil {
		fmt.Fprintf(os.Stderr, "error writing temp file: %v\n", err)
		os.Exit(1)
	}
	remoteTmp.Close()

	cmd := exec.CommandContext(ctx, "diff", "-u",
		"--label", "remote:"+remoteConfigPath,
		"--label", "local:"+localPath,
		remoteTmp.Name(), localTmp.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}

func readRemoteFile(ctx context.Context, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "fly", "ssh", "console", "-a", flyApp, "-C", "cat "+path)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("fly ssh failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

func writeRemoteFile(ctx context.Context, path string, data []byte) error {
	cmd := exec.CommandContext(ctx, "fly", "ssh", "console", "-a", flyApp, "-C",
		fmt.Sprintf("cat > %s", path))
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// updateGitHubConfigSecret updates the TCLAW_YAML GitHub secret so the config
// survives a volume wipe. Requires the gh CLI to be authenticated.
func updateGitHubConfigSecret(ctx context.Context, data []byte) error {
	cmd := exec.CommandContext(ctx, "gh", "secret", "set", "TCLAW_YAML",
		"--repo", "twindebank/tclaw",
		"--body", string(data))
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
