package router

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"tclaw/channel"
	"tclaw/store"
)

const sessionKeyPrefix = "session_"

// sessionKey returns a filesystem-safe key for persisting a channel's session ID.
// Slashes in channel IDs (e.g. socket paths) are replaced with underscores.
func sessionKey(chID channel.ChannelID) string {
	safe := strings.NewReplacer("/", "_", "\\", "_").Replace(string(chID))
	return sessionKeyPrefix + safe
}

func loadSession(ctx context.Context, s store.Store, chID channel.ChannelID) (string, error) {
	data, err := s.Get(ctx, sessionKey(chID))
	if err != nil {
		return "", fmt.Errorf("load session: %w", err)
	}
	if len(data) > 0 {
		slog.Info("resumed session", "channel", chID, "session_id", string(data))
		return string(data), nil
	}
	return "", nil
}

func saveSession(ctx context.Context, s store.Store, chID channel.ChannelID, id string) error {
	if err := s.Set(ctx, sessionKey(chID), []byte(id)); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}
