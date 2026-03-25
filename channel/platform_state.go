package channel

import (
	"encoding/json"
	"fmt"
)

// PlatformState is a sealed interface for platform-specific channel metadata
// that needs to persist across restarts. Each platform implements this with
// the data it needs (e.g. Telegram stores the chat ID so the bot can send
// messages before any inbound user message arrives).
type PlatformState interface {
	platformStateType() string
}

// TelegramPlatformState stores the chat ID for a Telegram channel so the bot
// can send outbound messages (agent responses, confirmations) before the user
// sends their first inbound message. For direct messages, chatID == userID.
type TelegramPlatformState struct {
	ChatID int64 `json:"chat_id"`
}

func (TelegramPlatformState) platformStateType() string { return "telegram" }

// platformStateEnvelope wraps PlatformState for JSON serialization with a type discriminator.
type platformStateEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// MarshalPlatformState serializes a PlatformState to JSON with a type tag.
// Returns nil for nil input.
func MarshalPlatformState(ps PlatformState) (json.RawMessage, error) {
	if ps == nil {
		return nil, nil
	}

	data, err := json.Marshal(ps)
	if err != nil {
		return nil, fmt.Errorf("marshal platform state data: %w", err)
	}

	envelope := platformStateEnvelope{
		Type: ps.platformStateType(),
		Data: data,
	}
	return json.Marshal(envelope)
}

// UnmarshalPlatformState deserializes a PlatformState from JSON.
// Returns nil for nil/empty input.
func UnmarshalPlatformState(raw json.RawMessage) (PlatformState, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var envelope platformStateEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal platform state envelope: %w", err)
	}

	switch envelope.Type {
	case "telegram":
		var ps TelegramPlatformState
		if err := json.Unmarshal(envelope.Data, &ps); err != nil {
			return nil, fmt.Errorf("unmarshal telegram platform state: %w", err)
		}
		return ps, nil
	default:
		return nil, fmt.Errorf("unknown platform state type: %q", envelope.Type)
	}
}
