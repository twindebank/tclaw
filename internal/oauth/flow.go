package oauth

import (
	"context"
	"fmt"

	"tclaw/internal/credential"

	"golang.org/x/oauth2"
)

// OAuth2Config holds the OAuth2 settings for a provider. Moved here from
// the old provider package — the oauth package is the natural owner.
type OAuth2Config struct {
	AuthURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
	ExtraParams  map[string]string // e.g. access_type=offline
}

// BuildAuthURL constructs the OAuth2 authorization URL the user must visit.
// state is an opaque token that maps back to the pending flow on callback.
func BuildAuthURL(cfg *OAuth2Config, state string, callbackURL string) string {
	oc := oauthConfig(cfg, callbackURL)

	var opts []oauth2.AuthCodeOption
	for k, v := range cfg.ExtraParams {
		opts = append(opts, oauth2.SetAuthURLParam(k, v))
	}

	return oc.AuthCodeURL(state, opts...)
}

// ExchangeCode trades an authorization code for access+refresh tokens.
func ExchangeCode(ctx context.Context, cfg *OAuth2Config, code string, callbackURL string) (*credential.OAuthTokens, error) {
	oc := oauthConfig(cfg, callbackURL)

	token, err := oc.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	return tokenToOAuthTokens(token), nil
}

// RefreshToken uses a refresh token to get a new access token.
func RefreshToken(ctx context.Context, cfg *OAuth2Config, refreshToken string) (*credential.OAuthTokens, error) {
	oc := oauthConfig(cfg, "")

	src := oc.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	token, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}

	return tokenToOAuthTokens(token), nil
}

func oauthConfig(cfg *OAuth2Config, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  cfg.AuthURL,
			TokenURL: cfg.TokenURL,
		},
		RedirectURL: redirectURL,
		Scopes:      cfg.Scopes,
	}
}

func tokenToOAuthTokens(token *oauth2.Token) *credential.OAuthTokens {
	return &credential.OAuthTokens{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
	}
}
