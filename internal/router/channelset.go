package router

import (
	"sync"

	"tclaw/internal/channel"
)

// ChannelSet is the single source of truth for the live channel map.
// Thread-safe. Replaces the overlapping staticChMap / currentChannels
// atomic.Pointer / allChMap pattern in waitAndStart.
type ChannelSet struct {
	mu       sync.RWMutex
	channels map[channel.ChannelID]channel.Channel
}

// NewChannelSet creates a ChannelSet initialized with the given channels.
func NewChannelSet(initial map[channel.ChannelID]channel.Channel) *ChannelSet {
	copied := make(map[channel.ChannelID]channel.Channel, len(initial))
	for id, ch := range initial {
		copied[id] = ch
	}
	return &ChannelSet{channels: copied}
}

// Snapshot returns a copy of the current channel map. Safe for concurrent
// iteration — the returned map is not shared.
func (cs *ChannelSet) Snapshot() map[channel.ChannelID]channel.Channel {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	snapshot := make(map[channel.ChannelID]channel.Channel, len(cs.channels))
	for id, ch := range cs.channels {
		snapshot[id] = ch
	}
	return snapshot
}

// Add adds channels to the set. Used by hot-add. If a channel ID already
// exists, it is overwritten.
func (cs *ChannelSet) Add(channels map[channel.ChannelID]channel.Channel) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	for id, ch := range channels {
		cs.channels[id] = ch
	}
}

// Replace replaces the entire channel map. Called at the start of each
// agent iteration to set the combined initial + agent-created channels.
func (cs *ChannelSet) Replace(channels map[channel.ChannelID]channel.Channel) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.channels = make(map[channel.ChannelID]channel.Channel, len(channels))
	for id, ch := range channels {
		cs.channels[id] = ch
	}
}

// Lookup returns a single channel by ID, or nil if not found.
func (cs *ChannelSet) Lookup(id channel.ChannelID) channel.Channel {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.channels[id]
}
