package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"path/filepath"

	"tclaw/agent"
	"tclaw/config"
	"tclaw/libraries/logbuffer"
	"tclaw/libraries/store"
	"tclaw/oauth"
	"tclaw/provider"
	"tclaw/router"
	"tclaw/version"
)

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

	// 50000 lines covers ~1 week of typical log volume and is loaded from
	// the persisted log file on startup so history survives deployments.
	logBuf := logbuffer.New(50000)
	logWriter := io.MultiWriter(os.Stderr, logBuf)
	slog.SetDefault(slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: slog.LevelDebug})))

	// Lower our OOM score so the kernel kills child processes (claude CLI)
	// before tclaw. Critical on memory-constrained VMs (256MB Fly.io).
	agent.ProtectFromOOM()

	cfg, err := config.Load(*configPath, config.Env(*envFlag))
	if err != nil {
		slog.Error("failed to load config", "path", *configPath, "err", err)
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

	// Build provider registry from config.
	reg := provider.NewRegistry()
	if cfg.Providers.Google != nil {
		reg.Register(provider.NewGoogleProvider(
			cfg.Providers.Google.ClientID,
			cfg.Providers.Google.ClientSecret,
		))
	}
	if cfg.Providers.Monzo != nil {
		reg.Register(provider.NewMonzoProvider(
			cfg.Providers.Monzo.ClientID,
			cfg.Providers.Monzo.ClientSecret,
		))
	}

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

	r := router.New(cfg.BaseDir, cfg.Env, reg, callback, cfg.Server.PublicURL, logBuf, *configPath)
	defer r.StopAll()

	for _, u := range cfg.Users {
		stateStore, err := store.NewFS(filepath.Join(cfg.BaseDir, string(u.ID), "state"))
		if err != nil {
			slog.Error("failed to create state store for channels", "user", u.ID, "err", err)
			os.Exit(1)
		}
		channels, err := r.BuildChannels(u.ID, u.Channels, cfg.Env, stateStore)
		if err != nil {
			slog.Error("failed to build channels", "user", u.ID, "err", err)
			os.Exit(1)
		}

		if err := r.Register(ctx, u.ToUserConfig(), channels, u.Channels); err != nil {
			slog.Error("failed to register user", "user", u.ID, "err", err)
			os.Exit(1)
		}
	}

	// Block until shutdown signal.
	<-ctx.Done()
	slog.Info("shutting down")
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
