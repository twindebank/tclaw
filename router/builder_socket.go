package router

import (
	"context"
	"fmt"
	"path/filepath"

	"tclaw/channel"
)

// SocketBuilder builds unix socket channel transports.
type SocketBuilder struct{}

func (SocketBuilder) Build(_ context.Context, params ChannelBuildParams) (channel.Channel, error) {
	if !params.Env.IsLocal() {
		return nil, fmt.Errorf("socket channels are not allowed in %q environment (no authentication)", params.Env)
	}
	socketPath := filepath.Join(params.BaseDir, string(params.UserID), params.ChannelCfg.Name+".sock")
	return channel.NewSocketServer(socketPath, params.ChannelCfg.Name, params.ChannelCfg.Description), nil
}
