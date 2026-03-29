package router

import (
	"context"
	"fmt"

	"tclaw/channel"
)

// StdioBuilder builds stdio channel transports.
type StdioBuilder struct{}

func (StdioBuilder) Build(_ context.Context, params ChannelBuildParams) (channel.Channel, error) {
	if !params.Env.IsLocal() {
		return nil, fmt.Errorf("stdio channels are not allowed in %q environment", params.Env)
	}
	return channel.NewStdio(), nil
}
