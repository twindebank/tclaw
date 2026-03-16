package repotools

import (
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/repo"
)

// Deps holds dependencies for repo exploration tools.
type Deps struct {
	Store       *repo.Store
	SecretStore secret.Store
	UserDir     string // base directory for this user (repos live under <UserDir>/repos/)
}

// RegisterTools adds repo exploration tools to the MCP handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(repoAddDef(), repoAddHandler(deps))
	handler.Register(repoSyncDef(), repoSyncHandler(deps))
	handler.Register(repoLogDef(), repoLogHandler(deps))
	handler.Register(repoListDef(), repoListHandler(deps))
	handler.Register(repoRemoveDef(), repoRemoveHandler(deps))
}
