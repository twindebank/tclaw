package documenttools

import (
	"context"

	"tclaw/internal/channel"
	"tclaw/internal/claudecli"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

// Package turns a markdown file the agent has written into a PDF and delivers
// it to the channel.
type Package struct {
	MemoryDir   string
	SecretStore secret.Store

	// SendFile delivers to whichever channel the turn is running on. Nil where
	// no channel can carry a file, which the tool reports rather than hiding.
	SendFile func(ctx context.Context, p channel.SendFileParams) (channel.MessageID, error)
}

func (p *Package) Name() string { return "document" }

func (p *Package) Description() string {
	return "Render a markdown file into a PDF and send it to the chat. Credential placeholders are filled in server-side, so a document can carry a password the agent never sees."
}

func (p *Package) Group() toolgroup.ToolGroup { return toolgroup.GroupChannelMessaging }

func (p *Package) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	// channel_management is documented as a superset of channel_messaging, and
	// groups are unioned from explicit lists rather than inherited
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		toolgroup.GroupChannelMessaging:  {"mcp__tclaw__document_*"},
		toolgroup.GroupChannelManagement: {"mcp__tclaw__document_*"},
	}
}

func (p *Package) RequiredSecrets() []toolpkg.SecretSpec { return nil }

func (p *Package) Info(_ context.Context, _ secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Group:       p.Group(),
		GroupInfo:   toolgroup.GroupInfo{Group: p.Group(), Description: "Sending messages and documents to channels."},
		Tools:       ToolNames(),
	}, nil
}

func (p *Package) Register(handler *mcp.Handler, ctx toolpkg.RegistrationContext) error {
	memoryDir := p.MemoryDir
	if memoryDir == "" {
		memoryDir = ctx.MemoryDir
	}
	secretStore := p.SecretStore
	if secretStore == nil {
		secretStore = ctx.SecretStore
	}

	RegisterTools(handler, Deps{
		MemoryDir:   memoryDir,
		SecretStore: secretStore,
		SendFile:    p.SendFile,
	})
	return nil
}

// ToolNames lists the tools this package registers.
func ToolNames() []string { return []string{ToolSendPDF} }
