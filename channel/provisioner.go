package channel

import "context"

// EphemeralProvisioner handles platform-specific resource creation and
// teardown for channels. Each channel type (Telegram, Slack, etc.) has its
// own provisioner implementation. The provisioner is called by channel_create
// (when no explicit token is provided) and channel_done/auto-cleanup.
type EphemeralProvisioner interface {
	// Provision creates the platform-specific resources for a channel
	// (e.g. mints a Telegram bot via BotFather). Returns the connection
	// credential (e.g. bot token) and teardown state to persist.
	Provision(ctx context.Context, name, purpose string) (*ProvisionResult, error)

	// Teardown cleans up platform-specific resources using the persisted
	// teardown state (e.g. deletes a Telegram bot). Returns an error if
	// cleanup fails — the caller must NOT delete the channel config when
	// teardown fails, to avoid orphaning platform resources.
	Teardown(ctx context.Context, state TeardownState) error

	// ConfirmTeardown sends a confirmation prompt to the channel's user and
	// blocks until the user explicitly approves the teardown. Returns an error
	// if the user rejects, the request times out, or the platform can't reach
	// the user. The token and platformState are used to contact the user via
	// the platform's messaging API.
	ConfirmTeardown(ctx context.Context, token string, platformState PlatformState) error
}

// ProvisionResult is returned by EphemeralProvisioner.Provision.
type ProvisionResult struct {
	// Token is the connection credential (bot token, webhook URL, etc.).
	Token string

	// TeardownState holds platform-specific state for later cleanup.
	TeardownState TeardownState

	// AllowedUsers are platform-specific user IDs to restrict access
	// (e.g. Telegram user IDs). May be nil if not applicable.
	AllowedUsers []int64
}
