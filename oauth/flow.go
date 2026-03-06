package oauth

import (
	"context"
	"fmt"

	"tclaw/connection"
	"tclaw/provider"

	"golang.org/x/oauth2"
)

// BuildAuthURL constructs the OAuth2 authorization URL the user must visit.
// state is an opaque token that maps back to the pending flow on callback.
func BuildAuthURL(cfg *provider.OAuth2Config, state string, callbackURL string) string {
	oc := oauthConfig(cfg, callbackURL)

	var opts []oauth2.AuthCodeOption
	for k, v := range cfg.ExtraParams {
		opts = append(opts, oauth2.SetAuthURLParam(k, v))
	}

	return oc.AuthCodeURL(state, opts...)
}

// ExchangeCode trades an authorization code for access+refresh tokens.
func ExchangeCode(ctx context.Context, cfg *provider.OAuth2Config, code string, callbackURL string) (*connection.Credentials, error) {
	oc := oauthConfig(cfg, callbackURL)

	token, err := oc.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	return tokenToCreds(token), nil
}

// RefreshToken uses a refresh token to get a new access token.
func RefreshToken(ctx context.Context, cfg *provider.OAuth2Config, refreshToken string) (*connection.Credentials, error) {
	oc := oauthConfig(cfg, "")

	src := oc.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	token, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}

	return tokenToCreds(token), nil
}

func oauthConfig(cfg *provider.OAuth2Config, redirectURL string) *oauth2.Config {
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

func tokenToCreds(token *oauth2.Token) *connection.Credentials {
	return &connection.Credentials{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
	}
}
