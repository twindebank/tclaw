package channel

import "context"

// EphemeralProvisioner handles platform-specific resource creation and
// teardown for channels. Each channel type (Telegram, Slack, etc.) has its
// own provisioner implementation. The provisioner is called by channel_create
// (when no explicit token is provided) and channel_done/auto-cleanup.
type EphemeralProvisioner interface {
	// IsReady returns true if the channel has everything it needs to run
	// (e.g. token in secret store, credentials configured). The reconciler
	// calls this to decide whether to provision or mark as needs_setup.
	IsReady(ctx context.Context, channelName string) bool

	// CanAutoProvision returns true if this provisioner can create platform
	// resources without user interaction (e.g. Telegram Client API is configured).
	CanAutoProvision() bool

	// ValidateCreate checks platform-specific constraints before provisioning.
	ValidateCreate(description string) error

	// Provision creates the platform-specific resources for a channel
	// (e.g. mints a Telegram bot via BotFather) and initiates any
	// platform-specific conversation setup (e.g. sending /start so the
	// bot can message users). Returns the connection credential and
	// teardown state to persist.
	Provision(ctx context.Context, params ProvisionParams) (*ProvisionResult, error)

	// Teardown cleans up platform-specific resources using the persisted
	// teardown state (e.g. deletes a Telegram bot). Returns an error if
	// cleanup fails — the caller must NOT delete the channel config when
	// teardown fails, to avoid orphaning platform resources.
	Teardown(ctx context.Context, state TeardownState) error

	// SendTeardownPrompt sends a confirmation prompt to the channel's user asking
	// them to confirm teardown by replying "yes". Returns immediately after
	// sending — does NOT wait for the user's response.
	SendTeardownPrompt(ctx context.Context, token string, platformState PlatformState) error

	// SendClosingMessage sends a brief acknowledgement after the user confirms
	// teardown, before the bot is deleted. Best-effort.
	SendClosingMessage(ctx context.Context, token string, platformState PlatformState) error

	// Notify sends an out-of-band message to the channel's user via the
	// platform. The provisioner uses its own configuration to determine
	// who to notify.
	Notify(ctx context.Context, token string, message string) error

	// PlatformResponseInfo returns platform-specific fields to include in tool
	// responses (e.g. bot username and link). Returns nil if no extra info.
	PlatformResponseInfo(teardownState TeardownState) map[string]any
}

// ProvisionerLookup returns the EphemeralProvisioner for a channel type, or nil
// if none is available. Used instead of a static map so provisioners registered
// during tool package initialization are visible without ordering constraints.
// A nil ProvisionerLookup is safe to call via the Get method.
type ProvisionerLookup func(ChannelType) EphemeralProvisioner

// Get returns the provisioner for the given type, or nil. Safe to call on a nil receiver.
func (f ProvisionerLookup) Get(ct ChannelType) EphemeralProvisioner {
	if f == nil {
		return nil
	}
	return f(ct)
}

// ProvisionParams is the input to EphemeralProvisioner.Provision.
type ProvisionParams struct {
	Name    string
	Purpose string
}

// ProvisionResult is returned by EphemeralProvisioner.Provision.
type ProvisionResult struct {
	Token         string
	TeardownState TeardownState
	PlatformState PlatformState
	AllowedUsers  []string
}
