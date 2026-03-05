package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	claude "github.com/character-ai/claude-agent-sdk-go"

	"personal-agent/agent"
	"personal-agent/channel"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ch := channel.NewSocketServer(channel.SocketPath)

	ag := agent.New(claude.Options{
		PermissionMode: claude.PermissionAcceptEdits,
	}, ch)

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	if err := ag.Run(ctx); err != nil {
		slog.Error("agent exited", "err", err)
		os.Exit(1)
	}
}
