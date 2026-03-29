package channel

import (
	"encoding/json"
	"fmt"
)

// TeardownState is a sealed interface for platform-specific ephemeral cleanup
// state. Each platform implements this with the data it needs for teardown
// (e.g. Telegram stores the bot username so it can be deleted via BotFather).
//
// To add a new teardown state type:
//  1. Define a struct implementing TeardownState (teardownType returns a unique string)
//  2. Call RegisterTeardownState in an init() or at startup
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

// teardownStateRegistry maps type discriminator strings to factory functions.
// Populated by RegisterTeardownState.
var teardownStateRegistry = map[string]func() TeardownState{}

func init() {
	// Register built-in teardown state types.
	RegisterTeardownState("telegram", func() TeardownState { return &TelegramTeardownState{} })
}

// RegisterTeardownState registers a new teardown state type for JSON
// deserialization. The typeName must match what teardownType() returns.
// Call this at init time or during startup for new channel types.
func RegisterTeardownState(typeName string, factory func() TeardownState) {
	if _, exists := teardownStateRegistry[typeName]; exists {
		panic(fmt.Sprintf("teardown state type %q already registered", typeName))
	}
	teardownStateRegistry[typeName] = factory
}

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

// UnmarshalTeardownState deserializes a TeardownState from JSON using the
// type registry. Returns nil for nil/empty input. Returns an error for
// unregistered types.
func UnmarshalTeardownState(raw json.RawMessage) (TeardownState, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var envelope teardownEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal teardown envelope: %w", err)
	}

	factory, ok := teardownStateRegistry[envelope.Type]
	if !ok {
		return nil, fmt.Errorf("unknown teardown state type: %q", envelope.Type)
	}

	ts := factory()
	if err := json.Unmarshal(envelope.Data, ts); err != nil {
		return nil, fmt.Errorf("unmarshal %s teardown state: %w", envelope.Type, err)
	}

	// Dereference pointer to return value type (factory returns pointer for
	// json.Unmarshal, but callers expect value types like TelegramTeardownState).
	return dereferenceTS(ts), nil
}

// dereferenceTS returns the underlying value if ts is a pointer to a
// TeardownState implementation. This preserves the existing behavior where
// callers use value types (e.g. TelegramTeardownState, not *TelegramTeardownState).
func dereferenceTS(ts TeardownState) TeardownState {
	switch v := ts.(type) {
	case *TelegramTeardownState:
		return *v
	default:
		return ts
	}
}
