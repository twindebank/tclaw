package bankingtools

import (
	"context"
	"fmt"
	"time"
)

// BankingPendingFlow implements oauth.PendingOAuthFlow for the Enable Banking
// bank authorization callback. When the user completes bank auth in their browser,
// the callback server dispatches the authorization code here.
type BankingPendingFlow struct {
	Client       *Client
	SessionStore *SessionStore
	BankName     string
	ASPSPID      string
	Country      string

	done chan struct{}

	// Set before done is closed.
	Result *BankSession
	Err    error
}

// NewBankingPendingFlow creates a new pending flow for bank authorization.
func NewBankingPendingFlow(client *Client, sessionStore *SessionStore, bankName string, aspspID string, country string) *BankingPendingFlow {
	return &BankingPendingFlow{
		Client:       client,
		SessionStore: sessionStore,
		BankName:     bankName,
		ASPSPID:      aspspID,
		Country:      country,
		done:         make(chan struct{}),
	}
}

// Complete exchanges the authorization code for a session and stores it.
func (f *BankingPendingFlow) Complete(ctx context.Context, code string, _ string) error {
	defer close(f.done)

	resp, err := f.Client.CreateSession(ctx, code)
	if err != nil {
		f.Err = fmt.Errorf("create banking session: %w", err)
		return f.Err
	}

	accountIDs := make([]string, len(resp.Accounts))
	for i, acc := range resp.Accounts {
		accountIDs[i] = acc.UID
	}

	session := BankSession{
		SessionID:  resp.SessionID,
		BankName:   f.BankName,
		ASPSPID:    f.ASPSPID,
		Country:    f.Country,
		AccountIDs: accountIDs,
		ValidUntil: resp.Access.ValidUntil,
		CreatedAt:  time.Now(),
	}

	if err := f.SessionStore.Add(ctx, session); err != nil {
		f.Err = fmt.Errorf("store banking session: %w", err)
		return f.Err
	}

	f.Result = &session
	return nil
}

// Fail records an error and closes the done channel.
func (f *BankingPendingFlow) Fail(err error) {
	f.Err = err
	close(f.done)
}

// DoneChan returns a channel that's closed when the flow completes.
func (f *BankingPendingFlow) DoneChan() <-chan struct{} {
	return f.done
}
