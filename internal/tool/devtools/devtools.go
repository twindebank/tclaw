package devtools

import (
	"tclaw/internal/dev"
	"tclaw/internal/libraries/logbuffer"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/user"
)

const (
	// githubTokenKey is the secret store key for the GitHub PAT.
	githubTokenKey = "github_token"

	// flyTokenKey is the secret store key for the Fly.io API token.
	flyTokenKey = "fly_api_token"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolStart, ToolStatus, ToolPR, ToolEnd, ToolCancel,
		ToolDeploy, ToolDeployed, ToolLog, ToolLogs, ToolBrowse,
		ToolPRChecks, ToolConfigGet, ToolConfigSet, ToolDisk,
	}
}

// Deps holds dependencies for dev workflow tools.
type Deps struct {
	Store       *dev.Store
	SecretStore secret.Store
	UserDir     string // base directory for this user (worktrees live under <UserDir>/worktrees/)
	UserID      user.ID
	LogBuffer   *logbuffer.Buffer // shared log ring buffer, nil if unavailable

	// ConfigPath is the path to the active tclaw.yaml. Copied into deploy
	// checkouts so remote Fly builds include the real config (it's gitignored).
	ConfigPath string

	// ActiveChannel returns the name of the channel currently being processed,
	// or "" when no channel is active. Used by dev_start to tag the session so
	// ephemeral cleanup can tear down sessions bound to an ephemeral channel.
	// May be nil in tests or when called outside an agent turn.
	ActiveChannel func() string
}

// RegisterTools adds dev workflow tools to the MCP handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(devStartDef(), devStartHandler(deps))
	handler.Register(devStatusDef(), devStatusHandler(deps))
	handler.Register(devPRDef(), devPRHandler(deps))
	handler.Register(devEndDef(), devEndHandler(deps))
	handler.Register(devCancelDef(), devCancelHandler(deps))
	handler.Register(deployDef(), deployHandler(deps))
	handler.Register(devDeployedDef(), devDeployedHandler(deps))
	handler.Register(devLogDef(), devLogHandler(deps))
	handler.Register(devLogsDef(), devLogsHandler(deps))
	handler.Register(devBrowseDef(), devBrowseHandler(deps))
	handler.Register(devPRChecksDef(), devPRChecksHandler(deps))
	handler.Register(configGetDef(), configGetHandler(deps))
	handler.Register(configSetDef(), configSetHandler(deps))
	handler.Register(devDiskDef(), devDiskHandler(deps))
}
