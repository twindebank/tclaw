package cli

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"tclaw/config"
	"tclaw/oauth"
	"tclaw/provider"
	"tclaw/router"
)

func runServe() {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "tclaw.yaml", "path to config file")
	dev := fs.Bool("dev", false, "hot-reload mode (requires air)")
	fs.Parse(os.Args[2:])

	if *dev {
		runServeDev()
		return
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "path", *configPath, "err", err)
		os.Exit(1)
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

	// Start the HTTP server (health checks, OAuth callbacks, Telegram webhooks).
	callback := oauth.NewCallbackServer(cfg.Server.Addr, cfg.Server.PublicURL)
	if err := callback.Start(); err != nil {
		slog.Error("failed to start http server", "err", err)
		os.Exit(1)
	}
	defer callback.Stop(context.Background())

	r := router.New(cfg.BaseDir, cfg.Env, reg, callback, cfg.Server.PublicURL)
	defer r.StopAll()

	for _, u := range cfg.Users {
		channels, err := r.BuildChannels(u.ID, u.Channels, cfg.Env)
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
