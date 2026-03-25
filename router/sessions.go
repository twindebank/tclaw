package router

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strings"

	"tclaw/channel"
	"tclaw/libraries/store"
)

// sessionKey returns a filesystem-safe key for persisting a channel's session ID.
// Slashes in channel IDs (e.g. socket paths) are replaced with underscores.
func sessionKey(chID channel.ChannelID) string {
	return strings.NewReplacer("/", "_", "\\", "_").Replace(string(chID))
}

func loadSession(ctx context.Context, s store.Store, chID channel.ChannelID) (string, error) {
	data, err := s.Get(ctx, sessionKey(chID))
	if err != nil {
		return "", fmt.Errorf("load session: %w", err)
	}
	sid := string(data)
	if sid == "" {
		return "", nil
	}
	if !validSessionID(sid) {
		slog.Warn("ignoring invalid session ID", "channel", chID, "len", len(sid))
		return "", nil
	}
	slog.Debug("resumed session", "channel", chID, "session_id", sid)
	return sid, nil
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

func saveSession(ctx context.Context, s store.Store, chID channel.ChannelID, id string) error {
	if err := s.Set(ctx, sessionKey(chID), []byte(id)); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

// loadChatID returns the persisted Telegram chat ID for a channel, or 0 if none.
func loadChatID(ctx context.Context, s store.Store, channelName string) int64 {
	data, err := s.Get(ctx, "chatid_"+channelName)
	if err != nil {
		slog.Error("failed to load chat ID", "channel", channelName, "err", err)
		return 0
	}
	if len(data) != 8 {
		return 0
	}
	return int64(binary.LittleEndian.Uint64(data))
}

// saveChatIDFunc returns a callback that persists a Telegram chat ID to the store.
func saveChatIDFunc(s store.Store, channelName string) func(int64) {
	return func(chatID int64) {
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, uint64(chatID))
		if err := s.Set(context.Background(), "chatid_"+channelName, buf); err != nil {
			slog.Error("failed to persist chat ID", "channel", channelName, "err", err)
		}
	}
}
