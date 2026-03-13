package providerutil

import (
	"context"
	"fmt"
	"strings"

	"tclaw/connection"
	"tclaw/oauth"
	"tclaw/provider"
)

// Deps holds dependencies shared by all provider tool packages (Google, Monzo, etc.).
type Deps struct {
	ConnID   connection.ConnectionID
	Manager  *connection.Manager
	Provider *provider.Provider
}

// ResolveDeps looks up the Deps for a connection ID from a connMap.
// The connIDStr comes from tool args; this validates it against known connections.
func ResolveDeps(connMap map[connection.ConnectionID]Deps, connIDStr string) (Deps, error) {
	connID := connection.ConnectionID(connIDStr)
	deps, ok := connMap[connID]
	if !ok {
		available := make([]string, 0, len(connMap))
		for id := range connMap {
			available = append(available, string(id))
		}
		return Deps{}, fmt.Errorf("unknown connection %q — available: %s", connIDStr, strings.Join(available, ", "))
	}
	return deps, nil
}

// AccessToken gets a valid access token for the connection, refreshing if needed.
func AccessToken(ctx context.Context, deps Deps) (string, error) {
	refreshFn := func(ctx context.Context, refreshToken string) (*connection.Credentials, error) {
		return oauth.RefreshToken(ctx, deps.Provider.OAuth2, refreshToken)
	}
	creds, err := deps.Manager.RefreshIfNeeded(ctx, deps.ConnID, refreshFn)
	if err != nil {
		return "", fmt.Errorf("get credentials for %s: %w", deps.ConnID, err)
	}
	return creds.AccessToken, nil
}
