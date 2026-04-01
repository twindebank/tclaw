package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"tclaw/internal/agent"
	"tclaw/internal/channel"
	"tclaw/internal/channel/stdiochannel"
	"tclaw/internal/claudecli"
	"tclaw/internal/config"
	"tclaw/internal/credential"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/all"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/user"
)

func runOneshot() {
	fs := flag.NewFlagSet("oneshot", flag.ExitOnError)
	configPath := fs.String("config", "tclaw.yaml", "path to config file")
	envFlag := fs.String("env", "local", "environment to load from config")
	userFlag := fs.String("user", "", "user ID (defaults to first user in config)")
	telegramMode := fs.Bool("telegram", false, "emulate Telegram formatting (split messages, HTML, expandable blockquotes)")
	debug := fs.Bool("debug", false, "log raw CLI event JSON")
	fs.Parse(os.Args[2:])

	message := strings.Join(fs.Args(), " ")
	if message == "" {
		fmt.Fprintln(os.Stderr, "usage: tclaw oneshot [flags] <message>")
		fmt.Fprintln(os.Stderr, "")
		fs.PrintDefaults()
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	cfg, err := config.Load(*configPath, config.Env(*envFlag))
	if err != nil {
		slog.Error("failed to load config", "path", *configPath, "err", err)
		os.Exit(1)
	}

	// Find the user.
	var userCfg *config.User
	if *userFlag != "" {
		for i := range cfg.Users {
			if string(cfg.Users[i].ID) == *userFlag {
				userCfg = &cfg.Users[i]
				break
			}
		}
		if userCfg == nil {
			slog.Error("user not found in config", "user", *userFlag)
			os.Exit(1)
		}
	} else {
		userCfg = &cfg.Users[0]
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Use the same per-user directories as serve so the agent has access
	// to existing memory, settings, and credentials.
	userDir := filepath.Join(cfg.BaseDir, string(userCfg.ID))
	homeDir := filepath.Join(userDir, "home")
	memoryDir := filepath.Join(userDir, "memory")
	secretsDir := filepath.Join(userDir, "secrets")

	// Seed memory if it doesn't exist.
	seedMemory(memoryDir, homeDir)

	// Set up secret store so the agent can find persisted API keys / setup tokens.
	secretStore, err := secret.Resolve(string(userCfg.ID), secretsDir, os.Getenv(secret.MasterKeyEnv))
	if err != nil {
		slog.Error("failed to create secret store", "err", err)
		os.Exit(1)
	}

	// Set up MCP server with tool registry so oneshot has the same tools as serve.
	stateDir := filepath.Join(userDir, "state")
	stateStore, err := store.NewFS(stateDir)
	if err != nil {
		slog.Error("failed to create state store", "err", err)
		os.Exit(1)
	}
	credMgr := credential.NewManager(stateStore, secretStore)
	mcpHandler := mcp.NewHandler()

	toolRegistry, _ := all.NewRegistry(all.Params{
		SecretStore:       secretStore,
		StateStore:        stateStore,
		UserDir:           userDir,
		UserID:            user.ID(userCfg.ID),
		Env:               config.Env(*envFlag),
		ConfigPath:        *configPath,
		CredentialManager: credMgr,
		ModelStore:        stateStore,
	})

	regCtx := toolpkg.RegistrationContext{
		SecretStore:        secretStore,
		Callback:           nil,
		CredentialManager:  credMgr,
		Registry:           toolRegistry,
		OnCredentialChange: func(packageName string) {},
	}

	// Register all packages.
	if regErr := toolRegistry.RegisterAll(mcpHandler, regCtx); regErr != nil {
		slog.Error("failed to register tool packages", "err", regErr)
		os.Exit(1)
	}

	mcpServer := mcp.NewServer(mcpHandler)
	mcpAddr, err := mcpServer.Start("127.0.0.1:0")
	if err != nil {
		slog.Error("failed to start mcp server", "err", err)
		os.Exit(1)
	}
	defer mcpServer.Stop(context.Background())

	mcpConfigDir := filepath.Join(userDir, "mcp-config")
	mcpConfigPath, err := mcp.GenerateConfigFile(mcpConfigDir, mcpAddr, mcpServer.Token(), nil)
	if err != nil {
		slog.Error("failed to generate mcp config", "err", err)
		os.Exit(1)
	}

	// Read per-user setup token from env (same as serve).
	setupTokenEnvVar := agent.SetupTokenEnvVarName(string(userCfg.ID))
	setupToken := os.Getenv(setupTokenEnvVar)

	systemPrompt := agent.BuildSystemPrompt(
		[]agent.ChannelInfo{{
			Name:   "oneshot",
			Type:   "stdio",
			Source: "static",
		}},
		nil, // no dev sessions in oneshot mode
		nil, // no notifications in oneshot mode
		userCfg.SystemPrompt,
		nil, // no onboarding in oneshot mode
	)

	// Create the oneshot channel. Its Done() cancels agentCtx to exit
	// after the first turn.
	agentCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch := stdiochannel.NewOneshot(message, cancel, *telegramMode)
	chMap := map[channel.ChannelID]channel.Channel{"oneshot": ch}

	msgs := channel.FanIn(agentCtx, chMap)

	debugEnabled := userCfg.Debug || *debug

	opts := agent.Options{
		PermissionMode: userCfg.PermissionMode,
		Model:          userCfg.Model,
		MaxTurns:       userCfg.MaxTurns,
		Debug:          debugEnabled,
		APIKey:         userCfg.APIKey,
		HomeDir:        homeDir,
		MemoryDir:      memoryDir,
		Channels:       chMap,
		Sessions:       make(map[channel.ChannelID]string),
		AllowedTools:   []claudecli.Tool{claudecli.ToolRead, claudecli.ToolBash, "mcp__tclaw__*"},
		MCPToolNames: func() []string {
			tools := mcpHandler.ListTools()
			names := make([]string, len(tools))
			for i, td := range tools {
				names[i] = td.Name
			}
			return names
		},
		MCPConfigPaths: map[channel.ChannelID]string{"oneshot": mcpConfigPath},
		SystemPrompt:   systemPrompt,
		SecretStore:    secretStore,
		Env:            cfg.Env,
		UserID:         string(userCfg.ID),
		SetupToken:     setupToken,
	}

	err = agent.RunWithMessages(agentCtx, opts, msgs)
	if err != nil && !errors.Is(err, agent.ErrIdleTimeout) && ctx.Err() == nil {
		slog.Error("agent error", "err", err)
		os.Exit(1)
	}
}

// seedMemory ensures the user's memory dir and CLAUDE.md symlink exist.
// Mirrors router.seedUserMemory but lives here to avoid a circular import.
func seedMemory(memoryDir, homeDir string) {
	memoryMDPath := filepath.Join(memoryDir, "CLAUDE.md")
	if _, err := os.Stat(memoryMDPath); os.IsNotExist(err) {
		if mkErr := os.MkdirAll(memoryDir, 0o700); mkErr != nil {
			slog.Error("failed to create memory dir", "err", mkErr)
		} else if wErr := os.WriteFile(memoryMDPath, []byte(agent.DefaultMemoryTemplate), 0o600); wErr != nil {
			slog.Error("failed to seed CLAUDE.md", "err", wErr)
		}
	}

	claudeDir := filepath.Join(homeDir, ".claude")
	symlinkPath := filepath.Join(claudeDir, "CLAUDE.md")
	if _, err := os.Lstat(symlinkPath); os.IsNotExist(err) {
		if mkErr := os.MkdirAll(claudeDir, 0o700); mkErr != nil {
			slog.Error("failed to create .claude dir", "err", mkErr)
		} else if linkErr := os.Symlink(filepath.Join("..", "..", "memory", "CLAUDE.md"), symlinkPath); linkErr != nil {
			slog.Error("failed to create CLAUDE.md symlink", "err", linkErr)
		}
	}
}
