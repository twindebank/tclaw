package channel

import (
	"encoding/json"
	"fmt"
)

// PlatformState is a sealed interface for platform-specific channel metadata
// that needs to persist across restarts. Each platform implements this with
// the data it needs (e.g. Telegram stores the chat ID so the bot can send
// messages before any inbound user message arrives).
//
// To add a new platform state type:
//  1. Define a struct implementing PlatformState (platformStateType returns a unique string)
//  2. Call RegisterPlatformState in an init() or at startup
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

// platformStateRegistry maps type discriminator strings to factory functions.
// Populated by RegisterPlatformState.
var platformStateRegistry = map[string]func() PlatformState{}

func init() {
	// Register built-in platform state types.
	RegisterPlatformState("telegram", func() PlatformState { return &TelegramPlatformState{} })
}

// RegisterPlatformState registers a new platform state type for JSON
// deserialization. The typeName must match what platformStateType() returns.
// Call this at init time or during startup for new channel types.
func RegisterPlatformState(typeName string, factory func() PlatformState) {
	if _, exists := platformStateRegistry[typeName]; exists {
		panic(fmt.Sprintf("platform state type %q already registered", typeName))
	}
	platformStateRegistry[typeName] = factory
}

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

// UnmarshalPlatformState deserializes a PlatformState from JSON using the
// type registry. Returns nil for nil/empty input. Returns an error for
// unregistered types.
func UnmarshalPlatformState(raw json.RawMessage) (PlatformState, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var envelope platformStateEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal platform state envelope: %w", err)
	}

	factory, ok := platformStateRegistry[envelope.Type]
	if !ok {
		return nil, fmt.Errorf("unknown platform state type: %q", envelope.Type)
	}

	ps := factory()
	if err := json.Unmarshal(envelope.Data, ps); err != nil {
		return nil, fmt.Errorf("unmarshal %s platform state: %w", envelope.Type, err)
	}

	// Dereference pointer to return value type (factory returns pointer for
	// json.Unmarshal, but callers expect value types like TelegramPlatformState).
	return dereferencePS(ps), nil
}

// dereferencePS returns the underlying value if ps is a pointer to a
// PlatformState implementation. This preserves the existing behavior where
// callers use value types (e.g. TelegramPlatformState, not *TelegramPlatformState).
func dereferencePS(ps PlatformState) PlatformState {
	switch v := ps.(type) {
	case *TelegramPlatformState:
		return *v
	default:
		return ps
	}
}
