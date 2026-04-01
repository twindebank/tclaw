package socketchannel

import (
	"context"
	"fmt"
	"path/filepath"

	"tclaw/internal/channel"
	"tclaw/internal/channel/channelpkg"
)

// Package implements channelpkg.Package for unix socket channels.
type Package struct{}

func (Package) Type() channel.ChannelType { return channel.TypeSocket }

func (Package) Build(_ context.Context, params channelpkg.BuildParams) (channel.Channel, error) {
	if !params.Env.IsLocal() {
		return nil, fmt.Errorf("socket channels are not allowed in %q environment (no authentication)", params.Env)
	}
	socketPath := filepath.Join(params.BaseDir, string(params.UserID), params.ChannelCfg.Name+".sock")
	return NewServer(socketPath, params.ChannelCfg.Name, params.ChannelCfg.Description, params.ChannelCfg.Purpose), nil
}

func (Package) Provisioner() channel.EphemeralProvisioner { return nil }
