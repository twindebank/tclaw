package router

import (
	"context"
	"net/http"

	"tclaw/channel"
	"tclaw/config"
	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/user"
)

// ChannelBuildParams holds everything a ChannelBuilder needs to construct a transport.
type ChannelBuildParams struct {
	ChannelCfg  config.Channel
	UserCfg     config.User
	UserID      user.ID
	Env         config.Env
	BaseDir     string
	SecretStore secret.Store
	StateStore  store.Store

	// Webhook support (may be zero if not configured).
	PublicURL       string
	RegisterHandler func(pattern string, handler http.Handler)
}

// ChannelBuilder constructs a live Channel transport from config. Each channel
// type (telegram, socket, etc.) implements this to own its platform-specific
// build logic.
type ChannelBuilder interface {
	Build(ctx context.Context, params ChannelBuildParams) (channel.Channel, error)
}
