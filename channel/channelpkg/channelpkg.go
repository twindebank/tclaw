// Package channelpkg defines the standard interface for channel packages.
//
// Every channel type (socket, stdio, telegram) implements the Package
// interface. The Registry collects all packages and provides Build() to
// construct channels and Provisioners() to discover ephemeral provisioners.
package channelpkg

import (
	"context"
	"net/http"

	"tclaw/channel"
	"tclaw/config"
	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/user"
)

// Package is the standard interface every channel type implements.
type Package interface {
	// Type returns the ChannelType constant (e.g. "telegram", "socket").
	Type() channel.ChannelType

	// Build constructs a live Channel transport from config.
	Build(ctx context.Context, params BuildParams) (channel.Channel, error)

	// Provisioner returns the EphemeralProvisioner for this channel type,
	// or nil if it doesn't support provisioning.
	Provisioner() channel.EphemeralProvisioner
}

// BuildParams holds everything a channel package needs to construct a transport.
type BuildParams struct {
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
