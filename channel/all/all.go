// Package all provides the complete list of channel packages that use the
// channelpkg.Registry for building channels. Each package owns its own build
// logic via Build() — the router doesn't need to know about any specific
// channel type.
package all

import (
	"tclaw/channel"
	"tclaw/channel/channelpkg"
	"tclaw/channel/socketchannel"
	"tclaw/channel/stdiochannel"
	"tclaw/channel/telegramchannel"
)

// NewRegistry returns a registry containing all channel packages. The telegram
// provisioner is optional — pass nil if Telegram Client API is not configured.
func NewRegistry(telegramProvisioner channel.EphemeralProvisioner) *channelpkg.Registry {
	return channelpkg.NewRegistry(
		&socketchannel.Package{},
		&stdiochannel.Package{},
		telegramchannel.NewPackage(telegramProvisioner),
	)
}
