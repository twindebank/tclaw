package telegramclient

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gotd/td/tg"

	"tclaw/channel"
	"tclaw/channel/telegramchannel"
)

// ChannelHistoryFunc returns a function that reads Telegram message history
// for a channel by name. It resolves the Telegram chat ID from the channel's
// runtime state and calls the MTProto API. Returns nil if the Package hasn't
// been registered yet (state is nil).
func (p *Package) ChannelHistoryFunc() func(ctx context.Context, channelName string, limit int) (json.RawMessage, error) {
	if p.state == nil || p.RuntimeState == nil {
		return nil
	}
	return func(ctx context.Context, channelName string, limit int) (json.RawMessage, error) {
		if err := ensureConnected(ctx, p.state); err != nil {
			return nil, fmt.Errorf("telegram client not connected: %w", err)
		}

		// Look up the Telegram chat ID from the channel's runtime state.
		rs, err := p.RuntimeState.Get(ctx, channelName)
		if err != nil {
			return nil, fmt.Errorf("get runtime state for %q: %w", channelName, err)
		}
		if !rs.PlatformState.HasPlatformState() || rs.PlatformState.Type != channel.PlatformTelegram {
			return nil, fmt.Errorf("channel %q has no Telegram platform state", channelName)
		}
		var platformState telegramchannel.TelegramPlatformState
		if err := rs.PlatformState.ParsePlatformData(&platformState); err != nil {
			return nil, fmt.Errorf("parse platform state for %q: %w", channelName, err)
		}
		if platformState.ChatID == 0 {
			return nil, fmt.Errorf("channel %q has no Telegram chat ID", channelName)
		}

		if limit <= 0 {
			limit = 50
		}

		// Bot private chats use InputPeerUser (chat ID == user ID in DMs).
		messages, err := p.state.client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:  &tg.InputPeerUser{UserID: platformState.ChatID},
			Limit: limit,
		})
		if err != nil {
			return nil, fmt.Errorf("get telegram history for %q: %w", channelName, err)
		}

		return json.Marshal(extractMessages(messages))
	}
}
