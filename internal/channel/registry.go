package channel

import "sync"

// RegistryEntry is the unified metadata for any channel.
type RegistryEntry struct {
	Info
	Links  []Link
	Parent string
}

// Registry provides a read view of all channel metadata. Entries are loaded
// from the config file and refreshed via Reload() after config mutations.
type Registry struct {
	mu      sync.RWMutex
	entries []RegistryEntry
	byName  map[string]RegistryEntry
}

// NewRegistry creates a registry with the given entries.
func NewRegistry(entries []RegistryEntry) *Registry {
	r := &Registry{}
	r.load(entries)
	return r
}

// Reload replaces all entries. Called after a config mutation adds, edits,
// or removes a channel.
func (r *Registry) Reload(entries []RegistryEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load(entries)
}

func (r *Registry) load(entries []RegistryEntry) {
	r.entries = entries
	r.byName = make(map[string]RegistryEntry, len(entries))
	for _, e := range entries {
		r.byName[e.Name] = e
	}
}

// All returns every channel as RegistryEntries.
func (r *Registry) All() []RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]RegistryEntry, len(r.entries))
	copy(result, r.entries)
	return result
}

// ByName returns the entry for a channel by name, or nil if not found.
func (r *Registry) ByName(name string) *RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.byName[name]; ok {
		return &e
	}
	return nil
}

// NameExists returns true if any channel has this name.
func (r *Registry) NameExists(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.byName[name]
	return ok
}

// Remove deletes a single entry by name. Called immediately after a channel is
// removed from config so the in-memory view stays consistent within the same
// agent turn — without this, channel_list would still show the deleted channel
// and channel_create with the same name would incorrectly return "already exists".
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byName[name]; !ok {
		return
	}
	delete(r.byName, name)
	filtered := make([]RegistryEntry, 0, len(r.entries)-1)
	for _, e := range r.entries {
		if e.Name != name {
			filtered = append(filtered, e)
		}
	}
	r.entries = filtered
}

// Links returns the outbound link map: source channel name → targets.
func (r *Registry) Links() map[string][]Link {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m := make(map[string][]Link)
	for _, e := range r.entries {
		if len(e.Links) > 0 {
			m[e.Name] = e.Links
		}
	}
	return m
}

// LifecycleChannelNames returns the names of channels with NotifyLifecycle set.
func (r *Registry) LifecycleChannelNames() map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make(map[string]bool)
	for _, e := range r.entries {
		if e.NotifyLifecycle {
			names[e.Name] = true
		}
	}
	return names
}
