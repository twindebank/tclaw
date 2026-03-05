package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tclaw/agent"
	"tclaw/channel"
	"tclaw/store"
)

func main() {
	// Set up logging first so everything below is captured.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	// Claude Code sets env vars that block nested sessions.
	// Unset them before we spawn any claude subprocess.
	for _, key := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"} {
		slog.Debug("unsetting env", "key", key, "was", os.Getenv(key))
		os.Unsetenv(key)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	storePath := os.Getenv("TCLAW_STORE_PATH")
	if storePath == "" {
		storePath = "/tmp/tclaw"
	}
	s, err := store.NewFS(storePath)
	if err != nil {
		slog.Error("failed to create store", "err", err)
		os.Exit(1)
	}

	ag := agent.New(ctx, agent.Options{
		PermissionMode: agent.PermissionAcceptEdits,
		Model:          agent.ModelSonnet46,
		Channel:        channel.NewSocketServer(channel.SocketPath),
		Store:          s,
		AllowedTools: []agent.Tool{
			agent.ToolBash,
			agent.ToolRead,
			agent.ToolEdit,
			agent.ToolWrite,
			agent.ToolGlob,
			agent.ToolGrep,
			agent.ToolLS,
			agent.ToolLSP,
			agent.ToolWebFetch,
			agent.ToolWebSearch,
			agent.ToolNotebookEdit,
			agent.ToolAgent,
			agent.ToolTask,
			agent.ToolTaskOutput,
			agent.ToolTaskStop,
			agent.ToolTodoWrite,
			agent.ToolToolSearch,
			agent.ToolSkill,
			agent.ToolAskUserQuestion,
			agent.ToolEnterPlanMode,
			agent.ToolExitPlanMode,
			agent.ToolEnterWorktree,
			agent.ToolListMcpResources,
			agent.ToolReadMcpResource,
		},
	})

	if err := ag.Run(ctx); err != nil {
		slog.Error("agent exited", "err", err)
		os.Exit(1)
	}
}
