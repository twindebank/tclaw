// Package all provides the complete list of tool packages that use the
// toolpkg.Registry for registration. Each package owns its own setup logic
// via Register() and OnCredentialSetChange() — the router doesn't need to
// know about any specific package.
package all

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"tclaw/channel"
	"tclaw/config"
	"tclaw/credential"
	"tclaw/dev"
	"tclaw/libraries/logbuffer"
	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/notification"
	"tclaw/oauth"
	"tclaw/onboarding"
	"tclaw/remotemcpstore"
	"tclaw/repo"
	"tclaw/schedule"
	"tclaw/tool/bankingtools"
	"tclaw/tool/channeltools"
	"tclaw/tool/credentialtools"
	"tclaw/tool/devtools"
	"tclaw/tool/google"
	"tclaw/tool/modeltools"
	"tclaw/tool/monzo"
	"tclaw/tool/notificationtools"
	"tclaw/tool/onboardingtools"
	"tclaw/tool/remotemcp"
	"tclaw/tool/repotools"
	"tclaw/tool/restauranttools"
	"tclaw/tool/scheduletools"
	"tclaw/tool/secretform"
	"tclaw/tool/telegramclient"
	tfl "tclaw/tool/tfl"
	"tclaw/tool/toolpkg"
	"tclaw/user"
)

// Params holds all dependencies needed to construct the full set of tool
// packages. The router populates this and passes it to NewRegistry.
type Params struct {
	// Shared infrastructure.
	SecretStore secret.Store
	StateStore  store.Store
	Callback    *oauth.CallbackServer
	UserDir     string
	UserID      user.ID
	Env         config.Env
	ConfigPath  string

	// Credential system.
	CredentialManager *credential.Manager
	ToolRegistry      *toolpkg.Registry // set after NewRegistry returns; only used for credentialtools

	// Channel tools.
	ChannelRegistry *channel.Registry
	RuntimeState    *channel.RuntimeStateStore
	OnChannelAdded  func(string)
	OnChannelChange func()
	ActivityTracker *channel.ActivityTracker
	ActiveChannel   func() string

	// Channel send deps.
	Links         func() map[string][]channel.Link
	CrossChOutput chan<- channel.TaggedMessage
	ChannelsFunc  func() map[channel.ChannelID]channel.Channel

	// Channel transcript deps.
	SessionStore *channel.SessionStore
	HomeDir      string
	MemoryDir    string

	// Schedule tools.
	ScheduleStore *schedule.Store
	Scheduler     *schedule.Scheduler

	// Notification tools.
	NotificationManager *notification.Manager

	// Dev tools.
	DevStore  *dev.Store
	LogBuffer *logbuffer.Buffer

	// Repo tools.
	RepoStore *repo.Store

	// Remote MCP tools.
	RemoteMCPManager *remotemcpstore.Manager
	ConfigUpdater    func(context.Context) error

	// Secret form tools.
	BaseURL         string
	RegisterHandler func(string, http.Handler)

	// Onboarding tools.
	OnboardingStore *onboarding.Store

	// Model tools.
	ModelStore store.Store
}

// NewRegistry returns a registry containing all tool packages, constructed
// with the given deps. Add new packages here — the router imports this
// package and calls NewRegistry() without needing to know about individual
// tool packages.
//
// Order matters: telegramclient must come before channeltools because
// channeltools needs the telegram provisioner from telegramclient.
//
// Returns the provisioners map shared with channeltools — callers must use
// this map (not build their own) so reconciliation in tool calls and router
// restarts use the same provisioner instances.
func NewRegistry(p Params) (*toolpkg.Registry, channel.ProvisionerLookup) {
	credPkg := &credentialtools.Package{
		CredentialManager: p.CredentialManager,
	}

	tgClientPkg := &telegramclient.Package{
		SecretStore:  p.SecretStore,
		StateStore:   p.StateStore,
		RuntimeState: p.RuntimeState,
	}

	// Lazy provisioner lookup — reads tgClientPkg.Provisioner at call time, so
	// it works regardless of whether Register() has been called yet.
	provisioners := channel.ProvisionerLookup(func(ct channel.ChannelType) channel.EphemeralProvisioner {
		if ct == channel.TypeTelegram && tgClientPkg.Provisioner != nil {
			return tgClientPkg.Provisioner
		}
		return nil
	})

	chPkg := &channeltools.Package{
		Registry:        p.ChannelRegistry,
		RuntimeState:    p.RuntimeState,
		Env:             p.Env,
		SecretStore:     p.SecretStore,
		ConfigPath:      p.ConfigPath,
		UserID:          p.UserID,
		OnChannelAdded:  p.OnChannelAdded,
		OnChannelChange: p.OnChannelChange,
		ActivityTracker: p.ActivityTracker,
		Provisioners:    provisioners,
		ActiveChannel:   p.ActiveChannel,
		Links:           p.Links,
		Output:          p.CrossChOutput,
		Channels:        p.ChannelsFunc,
		SessionStore:    p.SessionStore,
		HomeDir:         p.HomeDir,
		MemoryDir:       p.MemoryDir,
		// Lazy — tgClientPkg.state is nil until Register() runs, so we
		// call ChannelHistoryFunc() at invocation time, not construction time.
		TelegramHistory: func(ctx context.Context, channelName string, limit int) (json.RawMessage, error) {
			fn := tgClientPkg.ChannelHistoryFunc()
			if fn == nil {
				return nil, fmt.Errorf("telegram client not available — credentials may not be configured")
			}
			return fn(ctx, channelName, limit)
		},
	}

	reg := toolpkg.NewRegistry(
		// Credential system.
		credPkg,

		// Telegramclient before channeltools — sets Provisioner on its struct.
		tgClientPkg,

		// Channel management — uses provisioners map populated after registration.
		chPkg,

		// Credential providers (OAuth / API key).
		&google.Package{NotificationManager: p.NotificationManager},
		&monzo.Package{},
		&tfl.Package{SecretStore: p.SecretStore},
		&restauranttools.Package{SecretStore: p.SecretStore},
		&bankingtools.Package{
			SecretStore: p.SecretStore,
			StateStore:  p.StateStore,
			Callback:    p.Callback,
		},
		&devtools.Package{
			Store:       p.DevStore,
			LogBuffer:   p.LogBuffer,
			SecretStore: p.SecretStore,
			UserDir:     p.UserDir,
			UserID:      p.UserID,
			ConfigPath:  p.ConfigPath,
		},
		&repotools.Package{
			Store:       p.RepoStore,
			SecretStore: p.SecretStore,
			UserDir:     p.UserDir,
		},

		// Standard packages.
		&scheduletools.Package{
			Store:     p.ScheduleStore,
			Scheduler: p.Scheduler,
		},
		&notificationtools.Package{
			Manager: p.NotificationManager,
		},
		&onboardingtools.Package{
			Store:         p.OnboardingStore,
			ScheduleStore: p.ScheduleStore,
			Scheduler:     p.Scheduler,
		},
		&modeltools.Package{
			Store: p.ModelStore,
		},
		&remotemcp.Package{
			Manager:       p.RemoteMCPManager,
			Callback:      p.Callback,
			ConfigUpdater: p.ConfigUpdater,
		},
		&secretform.Package{
			SecretStore:     p.SecretStore,
			BaseURL:         p.BaseURL,
			RegisterHandler: p.RegisterHandler,
		},
	)

	// Set the registry on credentialtools now that it exists.
	credPkg.Registry = reg

	return reg, provisioners
}
