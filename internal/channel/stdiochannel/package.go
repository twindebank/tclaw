package stdiochannel

import (
	"context"
	"fmt"

	"tclaw/internal/channel"
	"tclaw/internal/channel/channelpkg"
)

// Package implements channelpkg.Package for stdio channels.
type Package struct{}

func (Package) Type() channel.ChannelType { return channel.TypeStdio }

func (Package) Build(_ context.Context, params channelpkg.BuildParams) (channel.Channel, error) {
	if !params.Env.IsLocal() {
		return nil, fmt.Errorf("stdio channels are not allowed in %q environment", params.Env)
	}
	return NewStdio(), nil
}

func (Package) Provisioner() channel.EphemeralProvisioner { return nil }
