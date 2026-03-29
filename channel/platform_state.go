package channel

// PlatformType identifies the channel platform.
type PlatformType string

const (
	PlatformTelegram PlatformType = "telegram"
)

// PlatformState holds platform-specific channel metadata that persists across
// restarts. The Type field is the discriminator — exactly one platform-specific
// pointer field should be non-nil, matching Type.
type PlatformState struct {
	Type PlatformType `json:"type"`

	// Telegram holds Telegram-specific state. Non-nil when Type == PlatformTelegram.
	Telegram *TelegramPlatformState `json:"telegram,omitempty"`
}

// TelegramPlatformState stores the chat ID for a Telegram channel so the bot
// can send outbound messages before the user sends their first inbound message.
// For direct messages, ChatID == userID.
type TelegramPlatformState struct {
	ChatID int64 `json:"chat_id"`
}

// NewTelegramPlatformState creates a PlatformState for a Telegram channel.
func NewTelegramPlatformState(chatID int64) PlatformState {
	return PlatformState{
		Type:     PlatformTelegram,
		Telegram: &TelegramPlatformState{ChatID: chatID},
	}
}

// HasPlatformState returns true if this state has been populated.
func (p PlatformState) HasPlatformState() bool { return p.Type != "" }

// TeardownState holds platform-specific state needed to clean up resources
// when a channel is deleted. The Type field is the discriminator — exactly one
// platform-specific pointer field should be non-nil, matching Type.
type TeardownState struct {
	Type PlatformType `json:"type"`

	// Telegram holds Telegram-specific teardown state. Non-nil when Type == PlatformTelegram.
	Telegram *TelegramTeardownState `json:"telegram,omitempty"`
}

// TelegramTeardownState holds the bot username created for this channel
// so it can be deleted via BotFather when the channel is torn down.
type TelegramTeardownState struct {
	BotUsername string `json:"bot_username"`
}

// NewTelegramTeardownState creates a TeardownState for a Telegram channel.
func NewTelegramTeardownState(botUsername string) TeardownState {
	return TeardownState{
		Type:     PlatformTelegram,
		Telegram: &TelegramTeardownState{BotUsername: botUsername},
	}
}

// HasTeardownState returns true if this state has been populated.
func (t TeardownState) HasTeardownState() bool { return t.Type != "" }
