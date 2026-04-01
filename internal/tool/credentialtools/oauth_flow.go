package credentialtools

import (
	"context"
	"fmt"

	"tclaw/internal/credential"
	"tclaw/internal/oauth"
)

// credentialPendingFlow implements oauth.PendingOAuthFlow for the unified
// credential system. It exchanges the OAuth code for tokens and stores them
// in the credential manager.
type credentialPendingFlow struct {
	setID    credential.CredentialSetID
	oauthCfg *oauth.OAuth2Config
	credMgr  *credential.Manager
	onChange func(packageName string)
	pkgName  string
	done     chan struct{}
	err      error
}

func (f *credentialPendingFlow) Complete(ctx context.Context, code string, callbackURL string) error {
	tokens, err := oauth.ExchangeCode(ctx, f.oauthCfg, code, callbackURL)
	if err != nil {
		return fmt.Errorf("code exchange failed: %w", err)
	}

	if err := f.credMgr.SetOAuthTokens(ctx, f.setID, tokens); err != nil {
		return fmt.Errorf("store oauth tokens: %w", err)
	}

	if f.onChange != nil {
		f.onChange(f.pkgName)
	}

	close(f.done)
	return nil
}

func (f *credentialPendingFlow) Fail(err error) {
	f.err = err
	close(f.done)
}

func (f *credentialPendingFlow) DoneChan() <-chan struct{} {
	return f.done
}
