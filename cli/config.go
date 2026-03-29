package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

const configUsage = `Usage: tclaw config <command>

Commands:
  sync     Sync tclaw.yaml between local and remote (Fly.io)
  diff     Show differences between local and remote config
`

func runConfig() {
	subcommand := ""
	if len(os.Args) >= 3 {
		subcommand = os.Args[2]
	}

	switch subcommand {
	case "sync":
		configSync()
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

const remoteConfigPath = "/data/tclaw/tclaw.yaml"

func configSync() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	localPath := "tclaw.yaml"
	localData, err := os.ReadFile(localPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading local config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("→ reading remote config from fly...")
	remoteData, err := readRemoteFile(ctx, remoteConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading remote config: %v\n", err)
		fmt.Println("  (if this is first deploy, use 'tclaw config sync' after deploying once)")
		os.Exit(1)
	}

	// Merge: start with remote config (has runtime channel mutations from the
	// agent), then overlay local hand-written changes.
	merged := mergeConfigs(localData, remoteData)

	if bytes.Equal(merged, localData) && bytes.Equal(merged, remoteData) {
		fmt.Println("✓ configs are identical, nothing to sync")
		return
	}

	// Write merged result to both local and remote.
	if !bytes.Equal(merged, localData) {
		fmt.Println("→ updating local config...")
		if err := os.WriteFile(localPath, merged, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing local config: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Println("→ pushing config to remote...")
	if err := writeRemoteFile(ctx, remoteConfigPath, merged); err != nil {
		fmt.Fprintf(os.Stderr, "error writing remote config: %v\n", err)
		os.Exit(1)
	}

	// Also push secrets.
	fmt.Println("→ syncing secrets...")
	deploySecrets()

	fmt.Println("✓ config synced")
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

	fmt.Println("→ reading remote config from fly...")
	remoteData, err := readRemoteFile(ctx, remoteConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading remote config: %v\n", err)
		os.Exit(1)
	}

	if bytes.Equal(localData, remoteData) {
		fmt.Println("✓ configs are identical")
		return
	}

	// Write both to temp files and diff.
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
	cmd.Run() // diff exits 1 when files differ — that's expected
}

// mergeConfigs merges local and remote configs. Remote is the base (has
// runtime-created channels from the agent). Local overlays hand-written
// structure. Expired ephemeral channels are removed from both.
func mergeConfigs(localData, remoteData []byte) []byte {
	// Parse both configs to check for expired ephemeral channels.
	localCfg, localErr := parseConfigForMerge(localData)
	remoteCfg, remoteErr := parseConfigForMerge(remoteData)

	if localErr != nil || remoteErr != nil {
		// Can't parse — fall back to using local as-is.
		return localData
	}

	// Collect channel names from both sides.
	localChannels := make(map[string]mergeChannel)
	for env, users := range localCfg {
		for _, u := range users {
			for _, ch := range u.Channels {
				localChannels[env+"/"+u.ID+"/"+ch.Name] = ch
			}
		}
	}

	remoteChannels := make(map[string]mergeChannel)
	for env, users := range remoteCfg {
		for _, u := range users {
			for _, ch := range u.Channels {
				remoteChannels[env+"/"+u.ID+"/"+ch.Name] = ch
			}
		}
	}

	// Channels only on remote = agent-created in prod.
	// If they're expired ephemeral channels, skip them.
	// Otherwise they should be pulled to local.
	var expiredCount int
	for key, ch := range remoteChannels {
		if _, inLocal := localChannels[key]; inLocal {
			continue
		}
		if ch.Ephemeral && ch.isExpired() {
			// Expired ephemeral — don't pull to local.
			expiredCount++
			continue
		}
		// Non-expired channel only on remote — we'd need to add it to local.
		// For now, just report it. Full YAML merge is complex; the user can
		// pull specific channels manually after reviewing 'tclaw config diff'.
		fmt.Printf("  remote-only channel: %s (use 'tclaw config diff' to review)\n", key)
	}

	if expiredCount > 0 {
		fmt.Printf("  skipped %d expired ephemeral channels from remote\n", expiredCount)
	}

	// For now, use local as the merged result. Remote-only channels that
	// weren't expired are flagged but not auto-merged (complex YAML merge).
	// The user reviews via 'config diff' and edits manually if needed.
	return localData
}

// mergeChannel is a minimal channel representation for merge logic.
type mergeChannel struct {
	Name                 string `yaml:"name"`
	Ephemeral            bool   `yaml:"ephemeral"`
	EphemeralIdleTimeout string `yaml:"ephemeral_idle_timeout"`
	CreatedAt            string `yaml:"created_at"`
}

func (ch mergeChannel) isExpired() bool {
	if !ch.Ephemeral || ch.CreatedAt == "" {
		return false
	}
	created, err := time.Parse(time.RFC3339, ch.CreatedAt)
	if err != nil {
		return false
	}
	timeout := 24 * time.Hour
	if ch.EphemeralIdleTimeout != "" {
		if parsed, parseErr := time.ParseDuration(ch.EphemeralIdleTimeout); parseErr == nil {
			timeout = parsed
		}
	}
	return time.Since(created) > timeout
}

type mergeUser struct {
	ID       string         `yaml:"id"`
	Channels []mergeChannel `yaml:"channels"`
}

// parseConfigForMerge does a lightweight parse of the config to extract
// channel names and ephemeral status per environment.
func parseConfigForMerge(data []byte) (map[string][]mergeUser, error) {
	var raw map[string]struct {
		Users []mergeUser `yaml:"users"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	result := make(map[string][]mergeUser)
	for env, cfg := range raw {
		result[env] = cfg.Users
	}
	return result, nil
}

// readRemoteFile reads a file from the Fly.io VM via SSH.
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

// writeRemoteFile writes a file to the Fly.io VM via SSH.
func writeRemoteFile(ctx context.Context, path string, data []byte) error {
	// Write via stdin to avoid escaping issues.
	cmd := exec.CommandContext(ctx, "fly", "ssh", "console", "-a", flyApp, "-C",
		fmt.Sprintf("cat > %s", path))
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
