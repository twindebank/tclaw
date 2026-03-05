package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	claude "github.com/character-ai/claude-agent-sdk-go"

	"tclaw/agent"
	"tclaw/channel"
)

func main() {
	// Set up logging first so everything below is captured.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	// Claude Code sets CLAUDECODE=1 which blocks nested sessions.
	// Unset it before we spawn any claude subprocess.
	slog.Debug("unsetting CLAUDECODE", "was", os.Getenv("CLAUDECODE"))
	os.Unsetenv("CLAUDECODE")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ch := channel.NewSocketServer(channel.SocketPath)

	ag := agent.New(claude.Options{
		PermissionMode: claude.PermissionAcceptEdits,
		Debug:          true, // logs claude subprocess stderr to our stderr
	}, ch)

	if err := ag.Run(ctx); err != nil {
		slog.Error("agent exited", "err", err)
		os.Exit(1)
	}
}
