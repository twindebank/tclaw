package providerutil

import (
	"context"
	"fmt"
	"strings"

	"tclaw/credential"
	"tclaw/oauth"
)

// Deps holds dependencies shared by all provider tool packages (Google, Monzo, etc.).
type Deps struct {
	CredSetID credential.CredentialSetID
	Manager   *credential.Manager

	// OAuthConfig holds the OAuth2 endpoints for token refresh. Built from
	// the tool package's CredentialSpec + stored client credentials.
	OAuthConfig *oauth.OAuth2Config
}

// ResolveDeps looks up the Deps for a credential set ID from a map.
// The idStr comes from tool args; this validates it against known sets.
func ResolveDeps(depsMap map[credential.CredentialSetID]Deps, idStr string) (Deps, error) {
	id := credential.CredentialSetID(idStr)
	deps, ok := depsMap[id]
	if !ok {
		available := make([]string, 0, len(depsMap))
		for id := range depsMap {
			available = append(available, string(id))
		}
		return Deps{}, fmt.Errorf("unknown credential set %q — available: %s", idStr, strings.Join(available, ", "))
	}
	return deps, nil
}

// AccessToken gets a valid access token for the credential set, refreshing if needed.
func AccessToken(ctx context.Context, deps Deps) (string, error) {
	refreshFn := func(ctx context.Context, refreshToken string) (*credential.OAuthTokens, error) {
		return oauth.RefreshToken(ctx, deps.OAuthConfig, refreshToken)
	}
	tokens, err := deps.Manager.RefreshIfNeeded(ctx, deps.CredSetID, refreshFn)
	if err != nil {
		return "", fmt.Errorf("get credentials for %s: %w", deps.CredSetID, err)
	}
	return tokens.AccessToken, nil
}
