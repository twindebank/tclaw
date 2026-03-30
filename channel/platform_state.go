package channel

import "encoding/json"

// PlatformType identifies the channel platform.
type PlatformType string

const (
	PlatformTelegram PlatformType = "telegram"
)

// PlatformState holds platform-specific channel metadata that persists across
// restarts. The Type field is the discriminator; Data holds the platform-specific
// payload as opaque JSON. Each platform package provides typed constructors and parsers.
type PlatformState struct {
	Type PlatformType    `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// NewPlatformState creates a PlatformState by marshalling the platform-specific data.
func NewPlatformState(platformType PlatformType, data any) PlatformState {
	raw, err := json.Marshal(data)
	if err != nil {
		panic("channel: marshal platform state: " + err.Error())
	}
	return PlatformState{Type: platformType, Data: raw}
}

// HasPlatformState returns true if this state has been populated.
func (p PlatformState) HasPlatformState() bool { return p.Type != "" }

// ParsePlatformData unmarshals the platform-specific Data into the given target.
func (p PlatformState) ParsePlatformData(target any) error {
	return json.Unmarshal(p.Data, target)
}

// TeardownState holds platform-specific state needed to clean up resources
// when a channel is deleted. The Type field is the discriminator; Data holds the
// platform-specific payload as opaque JSON.
type TeardownState struct {
	Type PlatformType    `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// NewTeardownState creates a TeardownState by marshalling the platform-specific data.
func NewTeardownState(platformType PlatformType, data any) TeardownState {
	raw, err := json.Marshal(data)
	if err != nil {
		panic("channel: marshal teardown state: " + err.Error())
	}
	return TeardownState{Type: platformType, Data: raw}
}

// HasTeardownState returns true if this state has been populated.
func (t TeardownState) HasTeardownState() bool { return t.Type != "" }

// ParseTeardownData unmarshals the platform-specific Data into the given target.
func (t TeardownState) ParseTeardownData(target any) error {
	return json.Unmarshal(t.Data, target)
}
