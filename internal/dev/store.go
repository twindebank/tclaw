package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"tclaw/internal/libraries/store"
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

// DeleteSessionsByChannel removes every session that was started from the
// given channel and returns the deleted session records so the caller can
// run any additional cleanup (e.g. removing worktree directories). A single
// channel may own many dev sessions (one per branch / concurrent piece of
// work), so this scans the full session map rather than looking up by name.
// Returns an empty slice if channelName is empty or no sessions match.
func (s *Store) DeleteSessionsByChannel(ctx context.Context, channelName string) ([]Session, error) {
	if channelName == "" {
		return nil, nil
	}
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	var removed []Session
	for branch, sess := range sessions {
		if sess.CreatedByChannel != channelName {
			continue
		}
		removed = append(removed, sess)
		delete(sessions, branch)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	if err := s.saveSessions(ctx, sessions); err != nil {
		return nil, err
	}
	return removed, nil
}

func (s *Store) saveSessions(ctx context.Context, sessions map[string]Session) error {
	data, err := json.Marshal(sessions)
	if err != nil {
		return fmt.Errorf("marshal sessions: %w", err)
	}
	return s.store.Set(ctx, sessionsKey, data)
}

// ResolveParams selects which session a dev tool should operate on.
type ResolveParams struct {
	// Session is the explicit branch name to resolve. Empty means auto-select
	// the single session owned by Channel.
	Session string

	// Channel scopes resolution to sessions started from this channel. A session
	// started from a different channel is never resolved — this is what stops a
	// dev_end/dev_cancel in one channel from tearing down another channel's work.
	// Empty (stdio, tests, or no active channel) disables scoping and matches any
	// session, preserving the original single-user behaviour.
	Channel string
}

// ResolveSession finds the session to operate on, scoped to the calling channel.
// With an explicit branch it returns that session only if the channel owns it;
// otherwise it auto-selects the channel's single session, erroring when the
// channel has zero or multiple sessions. See ResolveParams for scoping rules.
func (s *Store) ResolveSession(ctx context.Context, p ResolveParams) (*Session, error) {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no active dev sessions")
	}

	if p.Session != "" {
		sess, ok := sessions[p.Session]
		if !ok {
			return nil, fmt.Errorf("no active session for branch %q", p.Session)
		}
		if !sessionInScope(sess, p.Channel) {
			// Refuse to act across channel boundaries — the caller must switch to
			// the owning channel. This guards against ending the wrong session.
			return nil, fmt.Errorf("session %q belongs to channel %q, not this channel — switch to that channel to act on it", p.Session, sess.CreatedByChannel)
		}
		return &sess, nil
	}

	scoped := make(map[string]Session)
	for branch, sess := range sessions {
		if sessionInScope(sess, p.Channel) {
			scoped[branch] = sess
		}
	}
	if len(scoped) == 0 {
		return nil, fmt.Errorf("no active dev sessions for this channel")
	}
	if len(scoped) == 1 {
		for _, sess := range scoped {
			return &sess, nil
		}
	}

	branches := make([]string, 0, len(scoped))
	for b := range scoped {
		branches = append(branches, b)
	}
	sort.Strings(branches)
	return nil, fmt.Errorf("multiple active sessions in this channel — specify which one: %v", branches)
}

// sessionInScope reports whether a session may be acted on from the given channel.
// A channel-less session (stdio, tests, or created before channel tagging) and an
// empty scope both match anything, for backwards compatibility; otherwise the
// session's owning channel must match exactly.
func sessionInScope(sess Session, channel string) bool {
	if channel == "" || sess.CreatedByChannel == "" {
		return true
	}
	return sess.CreatedByChannel == channel
}

// ListSessionsForChannel returns the active sessions a channel may act on,
// applying the same scoping rules as ResolveSession. An empty channel returns
// all sessions.
func (s *Store) ListSessionsForChannel(ctx context.Context, channel string) (map[string]Session, error) {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	scoped := make(map[string]Session, len(sessions))
	for branch, sess := range sessions {
		if sessionInScope(sess, channel) {
			scoped[branch] = sess
		}
	}
	return scoped, nil
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
