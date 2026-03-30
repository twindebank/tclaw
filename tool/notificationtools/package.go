package notificationtools

import (
	"context"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/notification"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

// Package implements toolpkg.Package for notification management tools.
type Package struct {
	Manager *notification.Manager
}

func (p *Package) Name() string { return "notification" }
func (p *Package) Description() string {
	return "Subscribe to and manage push notifications (new emails, PR merges, etc.). Discover available notification types, subscribe channels, and list active subscriptions."
}
func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupNotifications }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		p.Group(): {"mcp__tclaw__notification_*"},
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec { return nil }

func (p *Package) Info(ctx context.Context, secretStore secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Subscribe to and manage push notifications."},
		Credentials: nil,
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, ctx toolpkg.RegistrationContext) error {
	RegisterTools(handler, Deps{Manager: p.Manager})
	return nil
}
