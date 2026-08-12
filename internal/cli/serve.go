package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"path/filepath"

	"tclaw/internal/agent"
	"tclaw/internal/config"
	"tclaw/internal/libraries/logbuffer"
	"tclaw/internal/oauth"
	"tclaw/internal/router"
	"tclaw/internal/tool/all"
	"tclaw/internal/version"
	"tclaw/internal/watchdog"
)

// volumeConfigPath is where the runtime config lives on the persistent Fly
// volume. Agent mutations (channel create/edit/delete) and config_set write
// here so they survive redeploys. The image-baked seed config at
// /etc/tclaw/tclaw.yaml is only copied here on first boot (or after a volume
// wipe) — it never overwrites an existing volume config.
const volumeConfigPath = "/data/tclaw.yaml"

// bootstrapConfig resolves the active config path. In production, the runtime
// config lives on the persistent volume — this is the source of truth. The
// seed config baked into the image is only copied to the volume on first boot
// (or after a volume wipe). It never overwrites an existing volume config.
func bootstrapConfig(seedPath, env string) string {
	if env != "prod" {
		return seedPath
	}

	if _, err := os.Stat(volumeConfigPath); err == nil {
		// Volume config exists, use it.
		slog.Info("using volume config", "path", volumeConfigPath)
		return volumeConfigPath
	}

	// First boot: copy seed config to volume.
	data, err := os.ReadFile(seedPath)
	if err != nil {
		slog.Error("failed to read seed config for bootstrap", "seed", seedPath, "err", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(filepath.Dir(volumeConfigPath), 0o755); err != nil {
		slog.Error("failed to create volume config directory", "err", err)
		os.Exit(1)
	}

	if err := os.WriteFile(volumeConfigPath, data, 0o644); err != nil {
		slog.Error("failed to bootstrap volume config from seed", "err", err)
		os.Exit(1)
	}

	slog.Info("bootstrapped volume config from seed", "seed", seedPath, "volume", volumeConfigPath)
	return volumeConfigPath
}

func runServe() {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "tclaw.yaml", "path to config file")
	envFlag := fs.String("env", "local", "environment to load from config (e.g. local, prod)")
	dev := fs.Bool("dev", false, "hot-reload mode (requires air)")
	fs.Parse(os.Args[2:])

	if *dev {
		runServeDev()
		return
	}

	// 50000 lines is loaded from the persisted log file on startup so history
	// survives deployments and restarts.
	logBuf := logbuffer.New(50000)
	logWriter := io.MultiWriter(os.Stderr, logBuf)
	slog.SetDefault(slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: slog.LevelDebug})))

	// Lower our OOM score so the kernel kills child processes (claude CLI)
	// before tclaw. Critical on memory-constrained VMs (256MB Fly.io).
	agent.ProtectFromOOM()

	activeConfigPath := bootstrapConfig(*configPath, *envFlag)

	cfg, err := config.Load(activeConfigPath, config.Env(*envFlag))
	if err != nil {
		slog.Error("failed to load config", "path", activeConfigPath, "err", err)
		os.Exit(1)
	}

	// Persist logs to the volume so they survive deployments. Rotate the file
	// at 20MB, then load the tail into the buffer before opening for writing.
	logPath := filepath.Join(cfg.BaseDir, "logs", "tclaw.log")
	if err := logbuffer.RotateIfNeeded(logPath); err != nil {
		slog.Warn("log file rotation failed, continuing without rotation", "err", err)
	}
	if historical, err := logbuffer.ReadTailLines(logPath, 50000); err != nil {
		slog.Warn("failed to load historical logs", "err", err)
	} else if len(historical) > 0 {
		logBuf.Load(historical)
		slog.Info("loaded historical log lines", "count", len(historical))
	}
	logFile, err := logbuffer.OpenLogFile(logPath)
	if err != nil {
		slog.Warn("failed to open log file, logs will not be persisted", "path", logPath, "err", err)
	} else {
		defer logFile.Close()
		slog.SetDefault(slog.New(slog.NewTextHandler(
			io.MultiWriter(os.Stderr, logBuf, logFile),
			&slog.HandlerOptions{Level: slog.LevelDebug},
		)))
		slog.Info("log file opened", "path", logPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start the HTTP server (health checks, OAuth callbacks, Telegram webhooks).
	callback := oauth.NewCallbackServer(cfg.Server.Addr, cfg.Server.PublicURL)
	if err := callback.Start(); err != nil {
		slog.Error("failed to start http server", "err", err)
		os.Exit(1)
	}
	defer callback.Stop(context.Background())

	// Expose the embedded git commit hash so external tools (e.g. dev_deployed)
	// can determine what's running without relying on deploy records.
	callback.Handle("/version", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, version.Commit)
	}))

	// Under Fly, self-restart if the process stops answering its own health
	// endpoint. Fly's check detects the wedge but never restarts the machine, so
	// without this a wedged process stays down until a human intervenes. Gated on
	// FLY_MACHINE_ID because a hard exit only self-heals where Fly restarts us;
	// locally it would just kill the dev process.
	if os.Getenv("FLY_MACHINE_ID") != "" {
		go watchdog.Run(ctx, watchdog.Config{HealthURL: healthProbeURL(callback.Addr())})
	}

	// Slot types name the subsystem that consumes the credential, so an
	// unknown one means a credential nothing will ever read — fail here rather
	// than let it look configured.
	if err := config.ValidateCredentialSlotTypes(cfg.CredentialSlots, all.CredentialSlotTypes()); err != nil {
		slog.Error("invalid credential slot config", "err", err)
		os.Exit(1)
	}

	r := router.New(cfg.BaseDir, cfg.Env, cfg.CredentialSlots, callback, cfg.Server.PublicURL, logBuf, activeConfigPath)
	defer r.StopAll()

	for _, u := range cfg.Users {
		if err := r.Register(ctx, u.ToUserConfig(), u.Channels); err != nil {
			slog.Error("failed to register user", "user", u.ID, "err", err)
			os.Exit(1)
		}
	}

	// Block until shutdown signal.
	<-ctx.Done()
	slog.Info("shutting down")
}

// healthProbeURL builds the loopback /healthz URL the watchdog polls from the
// HTTP server's bound address. The server may bind an unspecified host
// (0.0.0.0 / ::) in prod, so the watchdog always dials loopback, which the same
// listener serves regardless of bind interface.
func healthProbeURL(boundAddr string) string {
	_, port, err := net.SplitHostPort(boundAddr)
	if err != nil {
		// Unparseable address — probe it as given rather than guess a port.
		return fmt.Sprintf("http://%s/healthz", boundAddr)
	}
	return fmt.Sprintf("http://127.0.0.1:%s/healthz", port)
}

func runServeDev() {
	fmt.Println("→ starting agent (hot reload)...")
	os.MkdirAll("tmp", 0o755)
	cmd := exec.Command("air", "-c", ".air.agent.toml")
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
