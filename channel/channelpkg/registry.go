package channelpkg

import (
	"context"
	"fmt"

	"tclaw/channel"
)

// Registry collects channel packages and provides lookup by type.
type Registry struct {
	packages map[channel.ChannelType]Package
}

// NewRegistry creates a registry from the given packages. Panics on duplicate
// types to catch wiring bugs at startup.
func NewRegistry(packages ...Package) *Registry {
	m := make(map[channel.ChannelType]Package, len(packages))
	for _, pkg := range packages {
		ct := pkg.Type()
		if _, exists := m[ct]; exists {
			panic(fmt.Sprintf("channelpkg: duplicate channel type %q", ct))
		}
		m[ct] = pkg
	}
	return &Registry{packages: m}
}

// Build constructs a channel of the given type from config.
func (r *Registry) Build(ctx context.Context, channelType channel.ChannelType, params BuildParams) (channel.Channel, error) {
	pkg, ok := r.packages[channelType]
	if !ok {
		return nil, fmt.Errorf("unknown channel type %q", channelType)
	}
	return pkg.Build(ctx, params)
}

// Provisioners returns a map of channel types to their ephemeral provisioners.
// Only includes types that have a non-nil provisioner.
func (r *Registry) Provisioners() map[channel.ChannelType]channel.EphemeralProvisioner {
	result := make(map[channel.ChannelType]channel.EphemeralProvisioner)
	for ct, pkg := range r.packages {
		if p := pkg.Provisioner(); p != nil {
			result[ct] = p
		}
	}
	return result
}
