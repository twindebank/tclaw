package repotools

import (
	"context"
	"fmt"
	"time"

	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/repo"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{ToolAdd, ToolSync, ToolLog, ToolList, ToolRemove, ToolRequestAccess}
}

// Deps holds dependencies for repo exploration tools.
type Deps struct {
	Store       *repo.Store
	SecretStore secret.Store
	UserDir     string // base directory for this user (repos live under <UserDir>/repos/)

	// ActiveChannel names the channel whose turn is running. Repos scoped to
	// specific channels are hidden from every other channel. Nil means no
	// channel context is available, which fails closed against scoped repos.
	ActiveChannel func() string

	// RemoteURL returns the git proxy remote for a repo. Fetches go through the
	// proxy so credentials are injected server-side and never reach a clone's
	// .git/config, which sits in a directory bound into the sandbox.
	RemoteURL func(name string) string

	// ArmGrant sends the user a confirmation prompt for an access grant and
	// arms it, returning once the prompt is away. The agent cannot answer it.
	ArmGrant func(ctx context.Context, params GrantRequest) error
}

// GrantRequest describes an access grant awaiting the user's confirmation.
type GrantRequest struct {
	// Repo is the tracked repo's name.
	Repo string

	// Access is the tier being requested.
	Access repo.Access

	// Credential is the git credential slot label to authenticate with.
	Credential string

	// DropToReadOnlyAt withdraws the grant when it passes. Zero never expires.
	DropToReadOnlyAt time.Time

	// Reason is the agent's justification, shown in the prompt so the user is
	// deciding on a described change rather than a bare yes/no.
	Reason string
}

// visibleTo reports whether the running turn's channel may operate on r, and
// returns the channel name for error messages.
func (d Deps) visibleTo(r repo.TrackedRepo) (string, bool) {
	var channelName string
	if d.ActiveChannel != nil {
		channelName = d.ActiveChannel()
	}
	return channelName, r.VisibleTo(channelName)
}

// errNotVisible is the error returned when a tool is asked to operate on a repo
// scoped to other channels. It names the repo but not the channels allowed to
// see it — that would leak the scoping to a channel that shouldn't know.
func errNotVisible(name string) error {
	return fmt.Errorf("no tracked repo named %q", name)
}

// RegisterTools adds repo exploration tools to the MCP handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(repoAddDef(), repoAddHandler(deps))
	handler.Register(repoSyncDef(), repoSyncHandler(deps))
	handler.Register(repoLogDef(), repoLogHandler(deps))
	handler.Register(repoListDef(), repoListHandler(deps))
	handler.Register(repoRemoveDef(), repoRemoveHandler(deps))
	handler.Register(repoRequestAccessDef(), repoRequestAccessHandler(deps))
}
