package router

import (
	"context"
	"log/slog"

	"tclaw/agent"
	"tclaw/channel"
	"tclaw/claudecli"
	"tclaw/config"
	"tclaw/dev"
	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/queue"
	"tclaw/tool/modeltools"
)

// AgentOptionParams holds all inputs needed to construct agent.Options for
// one iteration of the agent restart loop.
type AgentOptionParams struct {
	UserCfg    config.User
	Env        config.Env
	ConfigPath string

	Dirs    UserDirs
	AddDirs []string

	Channels     map[channel.ChannelID]channel.Channel
	ChannelsFunc func() map[channel.ChannelID]channel.Channel
	Sessions     map[channel.ChannelID]string
	Queue        *queue.Queue

	ChannelToolOverrides map[channel.ChannelID]agent.ChannelToolPermissions
	MCPConfigPath        string
	MCPConfigPaths       map[channel.ChannelID]string
	MCPHandler           *mcp.Handler

	SystemPrompt string
	SetupToken   string
	SecretStore  secret.Store
	StateStore   store.Store
	DevStore     *dev.Store
	SessionStore store.Store

	ActivityTracker   *channel.ActivityTracker
	ActiveChannelName *string // pointer to the active channel name (atomic-updated)

	ChannelChangeNotify chan struct{}

	WorktreesDir string
	ReposDir     string
}

// BuildAgentOptions constructs agent.Options from the params.
func BuildAgentOptions(ctx context.Context, p AgentOptionParams) agent.Options {
	return agent.Options{
		PermissionMode: p.UserCfg.PermissionMode,
		Model:          p.UserCfg.Model,
		ModelFunc: func() claudecli.Model {
			return modeltools.LoadModel(p.StateStore, p.UserCfg.Model)
		},
		MaxTurns:  p.UserCfg.MaxTurns,
		Debug:     p.UserCfg.Debug,
		APIKey:    p.UserCfg.APIKey,
		HomeDir:   p.Dirs.Home,
		MemoryDir: p.Dirs.Memory,
		AddDirs:   p.AddDirs,
		AddDirsFunc: func() []string {
			dirs := []string{p.WorktreesDir, p.ReposDir}
			sessions, err := p.DevStore.ListSessions(ctx)
			if err != nil {
				slog.Error("failed to list dev sessions for add-dirs", "err", err)
				return dirs
			}
			for _, sess := range sessions {
				dirs = append(dirs, sess.WorktreeDir)
			}
			return dirs
		},
		Channels:     p.Channels,
		ChannelsFunc: p.ChannelsFunc,
		Sessions:     p.Sessions,
		Queue:        p.Queue,
		OnSessionUpdate: func(chID channel.ChannelID, sessionID string) {
			if saveErr := saveSession(ctx, p.SessionStore, chID, sessionID); saveErr != nil {
				slog.Error("failed to save session", "err", saveErr)
			}
		},
		OnTurnStart: func(channelName string) {
			if p.ActiveChannelName != nil {
				*p.ActiveChannelName = channelName
			}
			p.ActivityTracker.TurnStarted(channelName)
		},
		OnTurnEnd: func(channelName string) {
			p.ActivityTracker.TurnEnded(channelName)
		},
		AllowedTools:         p.UserCfg.AllowedTools,
		DisallowedTools:      p.UserCfg.DisallowedTools,
		ChannelToolOverrides: p.ChannelToolOverrides,
		MCPConfigPath:        p.MCPConfigPath,
		MCPConfigPaths:       p.MCPConfigPaths,
		MCPToolNames: func() []string {
			tools := p.MCPHandler.ListTools()
			names := make([]string, len(tools))
			for i, td := range tools {
				names[i] = td.Name
			}
			return names
		},
		SystemPrompt:    p.SystemPrompt,
		SecretStore:     p.SecretStore,
		ChannelChangeCh: p.ChannelChangeNotify,
		OnReset: func(level agent.ResetLevel) error {
			return resetUser(level, p.Dirs.Memory, p.Dirs.Home, p.Dirs.Sessions, p.Dirs.State, p.Dirs.Secrets, p.Dirs.MCPConfig)
		},
		Env:           p.Env,
		UserID:        string(p.UserCfg.ID),
		SetupToken:    p.SetupToken,
		HasProdConfig: config.HasEnv(p.ConfigPath, config.EnvProd),
	}
}
