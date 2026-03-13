package devtools

import (
	"tclaw/dev"
	"tclaw/libraries/secret"
	"tclaw/mcp"
)

const (
	// githubTokenKey is the secret store key for the GitHub PAT.
	githubTokenKey = "github_token"

	// flyTokenKey is the secret store key for the Fly.io API token.
	flyTokenKey = "fly_api_token"
)

// Deps holds dependencies for dev workflow tools.
type Deps struct {
	Store       *dev.Store
	SecretStore secret.Store
	UserDir     string // base directory for this user (worktrees live under <UserDir>/worktrees/)
}

// RegisterTools adds dev workflow tools to the MCP handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(devStartDef(), devStartHandler(deps))
	handler.Register(devStatusDef(), devStatusHandler(deps))
	handler.Register(devEndDef(), devEndHandler(deps))
	handler.Register(devCancelDef(), devCancelHandler(deps))
	handler.Register(deployDef(), deployHandler(deps))
}
