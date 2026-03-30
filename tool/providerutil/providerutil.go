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

// BuildDepsMap builds a deps map from resolved credential sets for OAuth
// provider packages. Only ready sets are included. For each ready set it reads
// the client_id and client_secret from the credential manager and constructs
// the OAuthConfig from the spec.
func BuildDepsMap(ctx context.Context, manager *credential.Manager, spec OAuthSpec, sets []ResolvedSet) (map[credential.CredentialSetID]Deps, error) {
	depsMap := make(map[credential.CredentialSetID]Deps)
	for _, s := range sets {
		if !s.Ready {
			continue
		}
		clientID, err := manager.GetField(ctx, s.ID, "client_id")
		if err != nil {
			return nil, fmt.Errorf("read client_id for %s: %w", s.ID, err)
		}
		clientSecret, err := manager.GetField(ctx, s.ID, "client_secret")
		if err != nil {
			return nil, fmt.Errorf("read client_secret for %s: %w", s.ID, err)
		}
		depsMap[s.ID] = Deps{
			CredSetID: s.ID,
			Manager:   manager,
			OAuthConfig: &oauth.OAuth2Config{
				AuthURL:      spec.AuthURL,
				TokenURL:     spec.TokenURL,
				ClientID:     clientID,
				ClientSecret: clientSecret,
				Scopes:       spec.Scopes,
				ExtraParams:  spec.ExtraParams,
			},
		}
	}
	return depsMap, nil
}

// OAuthSpec holds the OAuth2 endpoints needed to build an OAuth2Config.
// Mirrors toolpkg.OAuthSpec but avoids a dependency on toolpkg.
type OAuthSpec struct {
	AuthURL     string
	TokenURL    string
	Scopes      []string
	ExtraParams map[string]string
}

// ResolvedSet pairs a credential set with its readiness status.
// Mirrors toolpkg.ResolvedCredentialSet but avoids a dependency on toolpkg.
type ResolvedSet struct {
	ID    credential.CredentialSetID
	Ready bool
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
