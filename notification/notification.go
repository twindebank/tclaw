// Package notification provides a push-based event system for tool integrations.
//
// Tool packages that want to emit notifications implement the Notifier interface.
// The Manager persists subscriptions, delegates to packages for mechanism-specific
// logic, and routes emitted notifications to channels as TaggedMessages.
//
// The system is fully observable — the agent can list, subscribe, and unsubscribe
// across all packages via MCP tools.
package notification

import (
	"context"
	"encoding/json"
	"time"

	"tclaw/libraries/id"
)

// ExtraKeyManager is the key used to pass the Manager via RegistrationContext.Extra.
const ExtraKeyManager = "notification_manager"

// SubscriptionID uniquely identifies a notification subscription.
type SubscriptionID string

// Scope controls a subscription's lifetime.
type Scope string

const (
	// ScopeOneShot subscriptions are auto-removed after the first delivery.
	ScopeOneShot Scope = "one_shot"

	// ScopeCredential subscriptions live with a credential set and are removed
	// when the credential set is deleted.
	ScopeCredential Scope = "credential"

	// ScopePersistent subscriptions live until explicitly removed.
	ScopePersistent Scope = "persistent"
)

// NotificationType describes one kind of notification a package can produce.
// Declared by tool packages via the Notifier interface.
type NotificationType struct {
	// Name is the stable identifier, unique within a package.
	Name string `json:"name"`

	// Description explains what this notification watches for.
	Description string `json:"description"`

	// Scopes lists which scopes make sense for this notification type.
	Scopes []Scope `json:"scopes"`

	// Params describes what the agent needs to provide when subscribing.
	Params []NotificationParam `json:"params,omitempty"`

	// AutoSubscribe means the package creates this subscription automatically
	// (e.g. PR merge on dev_start). Listed for visibility but the agent
	// doesn't need to subscribe manually.
	AutoSubscribe bool `json:"auto_subscribe,omitempty"`
}

// NotificationParam describes a single parameter for a notification type.
type NotificationParam struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// Subscription is the persistent record linking an event source to a delivery target.
type Subscription struct {
	ID          SubscriptionID `json:"id"`
	Scope       Scope          `json:"scope"`
	ChannelName string         `json:"channel_name"`
	PackageName string         `json:"package_name"`
	TypeName    string         `json:"type_name"`

	// Config is opaque to the manager — only the owning package reads it.
	// Contains whatever the package needs to restart watching after a reboot.
	Config json.RawMessage `json:"config"`

	// CredentialSetID ties this subscription to a credential set.
	// Set when Scope == ScopeCredential.
	CredentialSetID string `json:"credential_set_id,omitempty"`

	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

// Notification is what a package emits when an event is detected.
type Notification struct {
	SubscriptionID SubscriptionID
	Text           string
}

// CancelFunc stops watching for a subscription.
type CancelFunc func()

// SubscribeParams holds the inputs for creating a subscription.
type SubscribeParams struct {
	TypeName        string
	ChannelName     string
	Scope           Scope
	CredentialSetID string
	Label           string
	Params          map[string]string
}

// SubscribeResult is returned by Notifier.Subscribe with the persisted
// subscription and any extra info for the caller.
type SubscribeResult struct {
	Subscription Subscription

	Cancel CancelFunc

	// Info holds extra data to return to the caller (e.g. webhook URL).
	Info map[string]string
}

// Notifier is optionally implemented by tool packages that support notifications.
type Notifier interface {
	// NotificationTypes returns what this package can watch for.
	NotificationTypes() []NotificationType

	// Subscribe starts watching. The package owns the mechanism entirely —
	// webhook, polling, whatever. Returns the subscription to persist.
	Subscribe(ctx context.Context, params SubscribeParams, emitter Emitter) (*SubscribeResult, error)

	// Resubscribe restarts watching from a persisted subscription after reboot.
	Resubscribe(ctx context.Context, sub Subscription, emitter Emitter) (CancelFunc, error)

	// Cancel stops watching for a subscription. Must be idempotent.
	Cancel(id SubscriptionID)
}

// Emitter is the callback packages use to deliver notifications to the agent.
type Emitter interface {
	Emit(ctx context.Context, notification Notification) error
}

// GenerateID creates a new unique subscription ID.
func GenerateID() SubscriptionID {
	return SubscriptionID(id.Generate("notif"))
}
