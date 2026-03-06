package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tclaw/config"
	"tclaw/oauth"
	"tclaw/provider"
	"tclaw/router"
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

	// Build provider registry from config.
	reg := provider.NewRegistry()
	if cfg.Providers.Gmail != nil {
		reg.Register(provider.NewGmailProvider(
			cfg.Providers.Gmail.ClientID,
			cfg.Providers.Gmail.ClientSecret,
		))
	}

	// Start OAuth callback server if any OAuth providers are configured.
	var callback *oauth.CallbackServer
	if cfg.Providers.Gmail != nil {
		callback = oauth.NewCallbackServer(cfg.OAuth.CallbackAddr)
		if err := callback.Start(); err != nil {
			slog.Error("failed to start oauth callback server", "err", err)
			os.Exit(1)
		}
		defer callback.Stop(context.Background())
	}

	r := router.New(cfg.BaseDir, reg, callback)
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
