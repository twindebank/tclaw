package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tclaw/config"
	"tclaw/router"
	"tclaw/secret"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	configPath := flag.String("config", "tclaw.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "path", *configPath, "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	r := router.New(cfg.BaseDir, os.Getenv(secret.MasterKeyEnv))
	defer r.StopAll()

	for _, u := range cfg.Users {
		channels, err := r.BuildChannels(u.ID, u.Channels)
		if err != nil {
			slog.Error("failed to build channels", "user", u.ID, "err", err)
			os.Exit(1)
		}

		if err := r.Register(ctx, u.ToUserConfig(), channels); err != nil {
			slog.Error("failed to register user", "user", u.ID, "err", err)
			os.Exit(1)
		}
	}

	// Block until shutdown signal.
	<-ctx.Done()
	slog.Info("shutting down")
}
