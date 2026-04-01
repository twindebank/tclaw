package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"tclaw/internal/libraries/store"
)

// SessionRecord tracks a single Claude CLI session for a channel.
type SessionRecord struct {
	SessionID string    `json:"session_id"`
	StartedAt time.Time `json:"started_at"`

	// Cleared is true when the session was explicitly reset (e.g. user typed "reset").
	// The record is kept for history but Current() skips it.
	Cleared bool `json:"cleared,omitempty"`
}

// SessionStore tracks all session IDs per channel over time, replacing the
// old single-value session store. The "current" session is the last non-cleared
// entry. Historical sessions are preserved so channel_transcript can read across
// session boundaries.
type SessionStore struct {
	mu    sync.Mutex
	store store.Store
}

// NewSessionStore creates a session store backed by the given store.
func NewSessionStore(s store.Store) *SessionStore {
	return &SessionStore{store: s}
}

// SessionKey returns a filesystem-safe key for a channel ID. Slashes in
// channel IDs (e.g. socket paths) are replaced with underscores. Exported
// so both router and channeltools can use the same key format.
func SessionKey(chID ChannelID) string {
	return strings.NewReplacer("/", "_", "\\", "_").Replace(string(chID))
}

// Current returns the current session ID for a channel, or "" if the most
// recent session was cleared (reset) or no sessions exist.
func (s *SessionStore) Current(ctx context.Context, channelKey string) (string, error) {
	records, err := s.List(ctx, channelKey)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}
	last := records[len(records)-1]
	if last.Cleared {
		return "", nil
	}
	return last.SessionID, nil
}

// SetCurrent updates the current session for a channel. If sessionID is empty,
// the current session is marked as cleared (session reset) but history is
// preserved. If sessionID is non-empty and differs from the current, a new
// record is appended.
func (s *SessionStore) SetCurrent(ctx context.Context, channelKey string, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load(ctx, channelKey)
	if err != nil {
		return err
	}

	if sessionID == "" {
		// Session reset — mark the latest non-cleared record as cleared.
		for i := len(records) - 1; i >= 0; i-- {
			if !records[i].Cleared {
				records[i].Cleared = true
				break
			}
		}
	} else {
		// New session — skip if already the latest.
		if len(records) > 0 {
			last := records[len(records)-1]
			if last.SessionID == sessionID && !last.Cleared {
				return nil
			}
		}
		records = append(records, SessionRecord{
			SessionID: sessionID,
			StartedAt: time.Now().UTC(),
		})
	}

	return s.save(ctx, channelKey, records)
}

// List returns all session records for a channel in chronological order.
func (s *SessionStore) List(ctx context.Context, channelKey string) ([]SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(ctx, channelKey)
}

// load reads and parses session records, handling migration from the old
// raw-string format (single session ID stored as plain text).
func (s *SessionStore) load(ctx context.Context, channelKey string) ([]SessionRecord, error) {
	data, err := s.store.Get(ctx, channelKey)
	if err != nil {
		return nil, fmt.Errorf("load session history for %q: %w", channelKey, err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	// Try JSON array first (new format).
	if data[0] == '[' {
		var records []SessionRecord
		if err := json.Unmarshal(data, &records); err != nil {
			return nil, fmt.Errorf("parse session history for %q: %w", channelKey, err)
		}
		return records, nil
	}

	// Old format: plain string session ID. Migrate transparently.
	sid := string(data)
	if sid == "" || !validSessionID(sid) {
		return nil, nil
	}
	slog.Info("migrating legacy session format", "channel_key", channelKey, "session_id", sid)
	records := []SessionRecord{{SessionID: sid, StartedAt: time.Now().UTC()}}
	if saveErr := s.save(ctx, channelKey, records); saveErr != nil {
		slog.Warn("failed to save migrated session", "channel_key", channelKey, "err", saveErr)
	}
	return records, nil
}

func (s *SessionStore) save(ctx context.Context, channelKey string, records []SessionRecord) error {
	data, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("marshal session history for %q: %w", channelKey, err)
	}
	if err := s.store.Set(ctx, channelKey, data); err != nil {
		return fmt.Errorf("save session history for %q: %w", channelKey, err)
	}
	return nil
}

// validSessionID checks that a session ID is non-empty, reasonable length,
// and contains no control characters.
func validSessionID(sid string) bool {
	if sid == "" || len(sid) > 256 {
		return false
	}
	for _, r := range sid {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
