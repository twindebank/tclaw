package telegramchannel

import "tclaw/internal/channel"

// TelegramPlatformState stores the chat ID for a Telegram channel so the bot
// can send outbound messages before the user sends their first inbound message.
// For direct messages, ChatID == userID.
type TelegramPlatformState struct {
	ChatID int64 `json:"chat_id"`
}

// NewPlatformState creates a channel.PlatformState for a Telegram channel.
func NewPlatformState(chatID int64) channel.PlatformState {
	return channel.NewPlatformState(channel.PlatformTelegram, &TelegramPlatformState{ChatID: chatID})
}

// ParsePlatformState extracts Telegram-specific state from a channel.PlatformState.
func ParsePlatformState(ps channel.PlatformState) (*TelegramPlatformState, error) {
	var ts TelegramPlatformState
	if err := ps.ParsePlatformData(&ts); err != nil {
		return nil, err
	}
	return &ts, nil
}

// TelegramTeardownState holds the bot username created for this channel
// so it can be deleted via BotFather when the channel is torn down.
type TelegramTeardownState struct {
	BotUsername string `json:"bot_username"`
}

// NewTeardownState creates a channel.TeardownState for a Telegram channel.
func NewTeardownState(botUsername string) channel.TeardownState {
	return channel.NewTeardownState(channel.PlatformTelegram, &TelegramTeardownState{BotUsername: botUsername})
}

// ParseTeardownState extracts Telegram-specific state from a channel.TeardownState.
func ParseTeardownState(ts channel.TeardownState) (*TelegramTeardownState, error) {
	var tts TelegramTeardownState
	if err := ts.ParseTeardownData(&tts); err != nil {
		return nil, err
	}
	return &tts, nil
}
