package dev

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/libraries/store"
)

const (
	sessionsKey       = "dev_sessions"
	repoURLKey        = "dev_repo_url"
	deployedCommitKey = "dev_deployed_commit"
	appURLKey         = "dev_app_url"
)

// Store manages dev session state, repo URL, and deployed commit tracking.
type Store struct {
	store store.Store
}

// NewStore creates a Store backed by the given key-value store.
func NewStore(s store.Store) *Store {
	return &Store{store: s}
}

// ListSessions returns all active dev sessions keyed by branch name.
func (s *Store) ListSessions(ctx context.Context) (map[string]Session, error) {
	data, err := s.store.Get(ctx, sessionsKey)
	if err != nil {
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	if data == nil {
		return make(map[string]Session), nil
	}
	var sessions map[string]Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, fmt.Errorf("unmarshal sessions: %w", err)
	}
	return sessions, nil
}

// GetSession returns a single session by branch name, or nil if not found.
func (s *Store) GetSession(ctx context.Context, branch string) (*Session, error) {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	sess, ok := sessions[branch]
	if !ok {
		return nil, nil
	}
	return &sess, nil
}

// PutSession creates or updates a session.
func (s *Store) PutSession(ctx context.Context, sess Session) error {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return err
	}
	sessions[sess.Branch] = sess
	return s.saveSessions(ctx, sessions)
}

// DeleteSession removes a session by branch name.
func (s *Store) DeleteSession(ctx context.Context, branch string) error {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return err
	}
	delete(sessions, branch)
	return s.saveSessions(ctx, sessions)
}

func (s *Store) saveSessions(ctx context.Context, sessions map[string]Session) error {
	data, err := json.Marshal(sessions)
	if err != nil {
		return fmt.Errorf("marshal sessions: %w", err)
	}
	return s.store.Set(ctx, sessionsKey, data)
}

// ResolveSession finds the session to operate on. If session is non-empty, it
// looks up that specific branch. If empty and there's exactly one active session,
// it returns that one. Returns an error if ambiguous or not found.
func (s *Store) ResolveSession(ctx context.Context, session string) (*Session, error) {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no active dev sessions")
	}

	if session != "" {
		sess, ok := sessions[session]
		if !ok {
			return nil, fmt.Errorf("no active session for branch %q", session)
		}
		return &sess, nil
	}

	if len(sessions) == 1 {
		for _, sess := range sessions {
			return &sess, nil
		}
	}

	var branches []string
	for b := range sessions {
		branches = append(branches, b)
	}
	return nil, fmt.Errorf("multiple active sessions — specify which one: %v", branches)
}

// GetRepoURL returns the cached repository URL, or empty if not set.
func (s *Store) GetRepoURL(ctx context.Context) (string, error) {
	data, err := s.store.Get(ctx, repoURLKey)
	if err != nil {
		return "", fmt.Errorf("read repo url: %w", err)
	}
	return string(data), nil
}

// SetRepoURL persists the repository URL.
func (s *Store) SetRepoURL(ctx context.Context, url string) error {
	return s.store.Set(ctx, repoURLKey, []byte(url))
}

// GetDeployedCommit returns the last-deployed commit hash, or empty if not set.
func (s *Store) GetDeployedCommit(ctx context.Context) (string, error) {
	data, err := s.store.Get(ctx, deployedCommitKey)
	if err != nil {
		return "", fmt.Errorf("read deployed commit: %w", err)
	}
	return string(data), nil
}

// SetDeployedCommit persists the deployed commit hash.
func (s *Store) SetDeployedCommit(ctx context.Context, hash string) error {
	return s.store.Set(ctx, deployedCommitKey, []byte(hash))
}

// GetAppURL returns the deployed app's base URL (e.g. "https://your-app.fly.dev"), or empty if not set.
func (s *Store) GetAppURL(ctx context.Context) (string, error) {
	data, err := s.store.Get(ctx, appURLKey)
	if err != nil {
		return "", fmt.Errorf("read app url: %w", err)
	}
	return string(data), nil
}

// SetAppURL persists the deployed app's base URL.
func (s *Store) SetAppURL(ctx context.Context, url string) error {
	return s.store.Set(ctx, appURLKey, []byte(url))
}
