package bankingtools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/internal/libraries/store"
)

const sessionsStoreKey = "banking_sessions"

// BankSession represents an active Enable Banking session with a specific bank.
// Each session provides access to one or more accounts at that bank.
type BankSession struct {
	SessionID  string    `json:"session_id"`
	BankName   string    `json:"bank_name"`
	ASPSPID    string    `json:"aspsp_id"`
	Country    string    `json:"country"`
	AccountIDs []string  `json:"account_ids"`
	ValidUntil time.Time `json:"valid_until"`
	CreatedAt  time.Time `json:"created_at"`
}

// IsExpired returns true if the session has expired and needs re-authorization.
func (s BankSession) IsExpired() bool {
	return time.Now().After(s.ValidUntil)
}

// SessionStore manages banking sessions persisted as a JSON array in the state store.
type SessionStore struct {
	store store.Store
}

// NewSessionStore creates a session store backed by the given store.
func NewSessionStore(s store.Store) *SessionStore {
	return &SessionStore{store: s}
}

// List returns all banking sessions.
func (s *SessionStore) List(ctx context.Context) ([]BankSession, error) {
	data, err := s.store.Get(ctx, sessionsStoreKey)
	if err != nil {
		return nil, fmt.Errorf("read banking sessions: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	var sessions []BankSession
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, fmt.Errorf("parse banking sessions: %w", err)
	}
	return sessions, nil
}

// Add appends a new banking session.
func (s *SessionStore) Add(ctx context.Context, session BankSession) error {
	sessions, err := s.List(ctx)
	if err != nil {
		return err
	}
	sessions = append(sessions, session)
	return s.save(ctx, sessions)
}

// Remove deletes a session by its session ID.
func (s *SessionStore) Remove(ctx context.Context, sessionID string) error {
	sessions, err := s.List(ctx)
	if err != nil {
		return err
	}

	filtered := sessions[:0]
	for _, sess := range sessions {
		if sess.SessionID != sessionID {
			filtered = append(filtered, sess)
		}
	}

	return s.save(ctx, filtered)
}

// FindByAccountID returns the session that contains the given account UID.
func (s *SessionStore) FindByAccountID(ctx context.Context, accountID string) (*BankSession, error) {
	sessions, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, sess := range sessions {
		for _, uid := range sess.AccountIDs {
			if uid == accountID {
				return &sess, nil
			}
		}
	}
	return nil, fmt.Errorf("no banking session found for account %s — connect a bank first with banking_connect", accountID)
}

func (s *SessionStore) save(ctx context.Context, sessions []BankSession) error {
	data, err := json.Marshal(sessions)
	if err != nil {
		return fmt.Errorf("marshal banking sessions: %w", err)
	}
	if err := s.store.Set(ctx, sessionsStoreKey, data); err != nil {
		return fmt.Errorf("write banking sessions: %w", err)
	}
	return nil
}
