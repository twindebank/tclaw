package channel

import (
	"context"
	"fmt"
)

// RegistryEntry is the unified metadata for any channel, regardless of source.
// Extends Info with links (which aren't part of the transport-level Info).
type RegistryEntry struct {
	Info
	Links []Link
}

// Registry provides a unified read view of all channel metadata (both static
// config and dynamic user-created channels). Mutations go through
// DynamicStore() for dynamic channels; static channels are immutable.
type Registry struct {
	static       []RegistryEntry
	staticByName map[string]RegistryEntry
	dynamic      *DynamicStore
}

// NewRegistry creates a registry over the given static entries and dynamic store.
func NewRegistry(staticEntries []RegistryEntry, dynamic *DynamicStore) *Registry {
	byName := make(map[string]RegistryEntry, len(staticEntries))
	for _, e := range staticEntries {
		byName[e.Name] = e
	}
	return &Registry{
		static:       staticEntries,
		staticByName: byName,
		dynamic:      dynamic,
	}
}

// All returns every channel (static + dynamic) as RegistryEntries.
func (r *Registry) All(ctx context.Context) ([]RegistryEntry, error) {
	result := make([]RegistryEntry, len(r.static))
	copy(result, r.static)

	dynamics, err := r.dynamic.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dynamic channels: %w", err)
	}
	for _, dc := range dynamics {
		result = append(result, DynamicToEntry(dc))
	}
	return result, nil
}

// ByName returns the entry for a channel by name, or nil if not found.
func (r *Registry) ByName(ctx context.Context, name string) (*RegistryEntry, error) {
	if e, ok := r.staticByName[name]; ok {
		return &e, nil
	}
	dc, err := r.dynamic.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if dc == nil {
		return nil, nil
	}
	entry := DynamicToEntry(*dc)
	return &entry, nil
}

// IsStatic returns true if the name belongs to a static channel.
func (r *Registry) IsStatic(name string) bool {
	_, ok := r.staticByName[name]
	return ok
}

// NameExists returns true if any channel (static or dynamic) has this name.
func (r *Registry) NameExists(ctx context.Context, name string) (bool, error) {
	if _, ok := r.staticByName[name]; ok {
		return true, nil
	}
	dc, err := r.dynamic.Get(ctx, name)
	if err != nil {
		return false, err
	}
	return dc != nil, nil
}

// Links returns the merged outbound link map: source channel name → targets.
func (r *Registry) Links(ctx context.Context) (map[string][]Link, error) {
	m := make(map[string][]Link)
	for _, e := range r.static {
		if len(e.Links) > 0 {
			m[e.Name] = e.Links
		}
	}
	dynamics, err := r.dynamic.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dynamic channels for links: %w", err)
	}
	for _, dc := range dynamics {
		if len(dc.Links) > 0 {
			m[dc.Name] = dc.Links
		}
	}
	return m, nil
}

// LifecycleChannelNames returns the names of channels with NotifyLifecycle set.
func (r *Registry) LifecycleChannelNames(ctx context.Context) (map[string]bool, error) {
	names := make(map[string]bool)
	for _, e := range r.static {
		if e.NotifyLifecycle {
			names[e.Name] = true
		}
	}
	dynamics, err := r.dynamic.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dynamic channels for lifecycle: %w", err)
	}
	for _, dc := range dynamics {
		if dc.NotifyLifecycle {
			names[dc.Name] = true
		}
	}
	return names, nil
}

// DynamicStore returns the underlying store for mutation operations
// (Add, Update, Remove). Channel tools use this for CRUD.
func (r *Registry) DynamicStore() *DynamicStore {
	return r.dynamic
}

// DynamicToEntry converts a DynamicChannelConfig to a RegistryEntry.
func DynamicToEntry(dc DynamicChannelConfig) RegistryEntry {
	return RegistryEntry{
		Info: Info{
			Type:            dc.Type,
			Name:            dc.Name,
			Description:     dc.Description,
			Source:          SourceDynamic,
			Role:            dc.Role,
			AllowedTools:    dc.AllowedTools,
			DisallowedTools: dc.DisallowedTools,
			NotifyLifecycle: dc.NotifyLifecycle,
		},
		Links: dc.Links,
	}
}
