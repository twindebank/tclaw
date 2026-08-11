package devtools

import (
	"tclaw/internal/credential"
	"tclaw/internal/dev"
	"tclaw/internal/libraries/credentialerror"
	"tclaw/internal/libraries/logbuffer"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/user"
)

// githubTokenKey addresses the default git credential slot. The dev workflow
// shares its GitHub token with repo monitoring and the knowledge base, so all
// three read one slot rather than each hardcoding a flat store key.
var githubTokenKey = credential.GitTokenKey(credential.DefaultLabel)

// flyTokenKey is the secret store key for the Fly.io API token.
const flyTokenKey = "fly_api_token"

// gitTokenCredentialField describes the missing GitHub token to the agent. It
// targets the credential slot rather than a bare key, because a secret form
// cannot address the cred/ namespace by key.
func gitTokenCredentialField() credentialerror.Field {
	field := credentialerror.SlotField(credential.GitType, credential.DefaultLabel,
		credential.GitTokenField, "GitHub Personal Access Token")
	field.Description = "Create at github.com/settings/tokens with 'repo' scope"
	return field
}

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

// activeChannelName returns the current channel name, or "" when unknown. It
// scopes session resolution so dev_end/dev_pr/dev_cancel only touch sessions
// started from the calling channel.
func (d Deps) activeChannelName() string {
	if d.ActiveChannel == nil {
		return ""
	}
	return d.ActiveChannel()
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
