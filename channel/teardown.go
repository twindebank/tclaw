package channel

import (
	"encoding/json"
	"fmt"
)

// TeardownState is a sealed interface for platform-specific ephemeral cleanup
// state. Each platform implements this with the data it needs for teardown
// (e.g. Telegram stores the bot username so it can be deleted via BotFather).
type TeardownState interface {
	// teardownType returns a string discriminator used for JSON serialization.
	teardownType() string
}

// TelegramTeardownState holds the bot username created for this channel
// so it can be deleted via BotFather when the channel is torn down.
type TelegramTeardownState struct {
	BotUsername string `json:"bot_username"`
}

func (TelegramTeardownState) teardownType() string { return "telegram" }

// teardownEnvelope wraps TeardownState for JSON serialization with a type discriminator.
type teardownEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// MarshalTeardownState serializes a TeardownState to JSON with a type tag.
// Returns nil for nil input (no teardown state).
func MarshalTeardownState(ts TeardownState) (json.RawMessage, error) {
	if ts == nil {
		return nil, nil
	}

	data, err := json.Marshal(ts)
	if err != nil {
		return nil, fmt.Errorf("marshal teardown data: %w", err)
	}

	envelope := teardownEnvelope{
		Type: ts.teardownType(),
		Data: data,
	}
	return json.Marshal(envelope)
}

// UnmarshalTeardownState deserializes a TeardownState from JSON.
// Returns nil for nil/empty input.
func UnmarshalTeardownState(raw json.RawMessage) (TeardownState, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var envelope teardownEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal teardown envelope: %w", err)
	}

	switch envelope.Type {
	case "telegram":
		var ts TelegramTeardownState
		if err := json.Unmarshal(envelope.Data, &ts); err != nil {
			return nil, fmt.Errorf("unmarshal telegram teardown state: %w", err)
		}
		return ts, nil
	default:
		return nil, fmt.Errorf("unknown teardown state type: %q", envelope.Type)
	}
}
