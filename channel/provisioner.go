package channel

import "context"

// EphemeralProvisioner handles platform-specific resource creation and
// teardown for channels. Each channel type (Telegram, Slack, etc.) has its
// own provisioner implementation. The provisioner is called by channel_create
// (when no explicit token is provided) and channel_done/auto-cleanup.
type EphemeralProvisioner interface {
	// ValidateCreate checks platform-specific constraints before provisioning.
	// Called by channel_create with the user-provided args so each platform can
	// enforce its own requirements (e.g. Telegram needs at least one allowed user).
	ValidateCreate(allowedUsers []int64, description string) error

	// Provision creates the platform-specific resources for a channel
	// (e.g. mints a Telegram bot via BotFather). Returns the connection
	// credential (e.g. bot token) and teardown state to persist.
	Provision(ctx context.Context, name, purpose string) (*ProvisionResult, error)

	// Teardown cleans up platform-specific resources using the persisted
	// teardown state (e.g. deletes a Telegram bot). Returns an error if
	// cleanup fails — the caller must NOT delete the channel config when
	// teardown fails, to avoid orphaning platform resources.
	Teardown(ctx context.Context, state TeardownState) error

	// SendTeardownPrompt sends a confirmation prompt to the channel's user asking
	// them to confirm teardown by replying "yes". Returns immediately after
	// sending — does NOT wait for the user's response. The router intercepts the
	// reply asynchronously via the PendingDone flag on the channel config.
	SendTeardownPrompt(ctx context.Context, token string, platformState PlatformState) error

	// SendClosingMessage sends a brief acknowledgement to the channel immediately
	// after the user confirms teardown ("yes"). Called before platform teardown so
	// the bot can still send messages. Best-effort — callers should log errors but
	// not abort teardown if this fails.
	SendClosingMessage(ctx context.Context, token string, platformState PlatformState) error

	// Notify sends an out-of-band message to channel users via the platform.
	// Returns number of users notified. Used by channel_notify tool.
	Notify(ctx context.Context, token string, allowedUsers []int64, message string) (int, error)

	// PlatformResponseInfo returns platform-specific fields to include in tool
	// responses (e.g. {"platform_link": "https://t.me/bot", "platform_username": "bot"}).
	// Returns nil if there's no extra info to include.
	PlatformResponseInfo(teardownState TeardownState) map[string]any
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
